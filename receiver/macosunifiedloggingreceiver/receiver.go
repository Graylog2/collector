// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver // import "github.com/Graylog2/collector/receiver/macosunifiedloggingreceiver"

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/extension/xextension/storage"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
	"go.uber.org/zap"
)

const (
	startLayout    = "2006-01-02 15:04:05"
	flushBatchSize = 1000
	maxLineBytes   = 1024 * 1024
)

type unifiedLoggingReceiver struct {
	cfg      *Config
	id       component.ID
	logger   *zap.Logger
	consumer consumer.Logs
	runner   logRunner
	cursor   *cursor
	cadence  *cadence
	storage  storage.Client
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	now      func() time.Time
	// maxLineBytes caps a single ndjson line; longer lines are skipped. A field (not the
	// const directly) so tests can exercise the skip path without a multi-megabyte line.
	maxLineBytes int
}

func newUnifiedLoggingReceiver(cfg *Config, set receiver.Settings, consumer consumer.Logs, runner logRunner) *unifiedLoggingReceiver {
	minI := cmp.Or(cfg.MinPollInterval, time.Second)
	maxI := cmp.Or(cfg.MaxPollInterval, 30*time.Second)
	return &unifiedLoggingReceiver{
		cfg:          cfg,
		id:           set.ID,
		logger:       set.Logger,
		consumer:     consumer,
		runner:       runner,
		cursor:       newCursor(predicateHash(cfg.Predicate)),
		cadence:      newCadence(minI, maxI),
		now:          time.Now,
		maxLineBytes: maxLineBytes,
	}
}

func (r *unifiedLoggingReceiver) Start(_ context.Context, host component.Host) error {
	if r.cfg.ArchivePath == "" {
		client, err := getStorageClient(context.Background(), host, r.cfg.StorageID, r.id)
		if err != nil {
			return err
		}
		r.storage = client
		if data, err := client.Get(context.Background(), cursorStorageKey); err == nil && len(data) > 0 {
			switch c, lerr := loadCursor(data); {
			case lerr != nil:
				r.logger.Warn("could not load persisted cursor; starting fresh", zap.Error(lerr))
			case c.predicateHash != r.cursor.predicateHash:
				r.logger.Info("predicate changed since last run; discarding persisted cursor and starting fresh",
					zap.String("persisted", c.predicateHash), zap.String("current", r.cursor.predicateHash))
			default:
				r.cursor = c
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Go(func() {
		r.readLogs(ctx)
	})
	return nil
}

func (r *unifiedLoggingReceiver) Shutdown(ctx context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
	if r.storage != nil {
		return r.storage.Close(ctx)
	}
	return nil
}

func (r *unifiedLoggingReceiver) readLogs(ctx context.Context) {
	if r.cfg.ArchivePath != "" {
		r.readFromArchive(ctx)
		return
	}
	for {
		count, err := r.pollOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.Error("log poll failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.cadence.next(count)):
		}
	}
}

// startArgValue computes the next --start. A persisted cursor is honored as-is so no gap is
// ever skipped on resume; max_log_age (or an explicit start_time) bounds only a cold start.
func (r *unifiedLoggingReceiver) startArgValue() string {
	cur := r.cursor.startArg()
	if cur == "" {
		// Cold start: bound the initial read.
		if r.cfg.StartTime != "" {
			return r.cfg.StartTime
		}
		return r.now().UTC().Add(-r.cfg.MaxLogAge).Format(startLayout)
	}
	// Resume: read from the cursor even if it predates max_log_age, so an outage longer than
	// max_log_age does not silently drop the gap. Warn when it does — the store may already
	// have aged out part of that gap (a source limit we cannot recover from).
	if floor := r.now().UTC().Add(-r.cfg.MaxLogAge).Format(startLayout); cur < floor {
		r.logger.Warn("resuming from a cursor older than max_log_age; logs in the gap may have aged out of the store",
			zap.String("cursor", cur), zap.String("max_log_age_floor", floor))
	}
	return cur
}

func (r *unifiedLoggingReceiver) liveArgs(start string) []string {
	args := []string{"show", "--style", "ndjson", "--start", start + "+0000"}
	if r.cfg.Predicate != "" {
		args = append(args, "--predicate", r.cfg.Predicate)
	}
	return args
}

func (r *unifiedLoggingReceiver) pollOnce(ctx context.Context) (int, error) {
	// A per-poll context so an early exit (consume rejection) terminates the still-running
	// log subprocess instead of leaking it until the whole poll drains.
	pollCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stdout, wait, err := r.runner.Run(pollCtx, r.liveArgs(r.startArgValue()))
	if err != nil {
		return 0, fmt.Errorf("failed to start log: %w", err)
	}

	r.cursor.beginPoll()
	emitted := 0
	var consumeErr error

	// Events are buffered per whole second and delivered as a unit. The cursor advances over
	// a second only after its ConsumeLogs is accepted (recordDelivered), so a rejected
	// second is never skipped: the next poll re-reads it from the durable source, and the
	// existing boundary-second dedup suppresses the records already delivered.
	var pending []*logEvent
	curSec := ""
	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		logs, records := newBatch()
		for _, e := range pending {
			e.setLogRecord(records.AppendEmpty(), r.now())
		}
		if cerr := r.consumer.ConsumeLogs(pollCtx, logs); cerr != nil {
			consumeErr = cerr
			return false
		}
		r.cursor.recordDelivered(pending)
		emitted += len(pending)
		pending = pending[:0]
		return true
	}

	br := bufio.NewReaderSize(stdout, r.maxLineBytes)
	var scanErr error
readLoop:
	for {
		if pollCtx.Err() != nil {
			break
		}
		line, oversized, rerr := nextLine(br)
		if oversized > 0 {
			// A single line longer than the cap would otherwise stall the reader forever;
			// skip it (with a diagnostic) so the poll keeps making forward progress.
			r.logger.Warn("skipping oversized log line",
				zap.Int("bytes", oversized), zap.Int("max_bytes", r.maxLineBytes))
		}
		if len(line) > 0 {
			e, perr := parseLogEvent(line)
			switch {
			case perr != nil:
				r.logger.Warn("failed to parse log line", zap.Error(perr))
			case e == nil:
				// non-event (footer, blank line)
			default:
				if sec := e.utcSecondClamped; sec != curSec {
					if !flush() { // deliver the now-complete second before moving on
						break readLoop
					}
					curSec = sec
				}
				if r.cursor.shouldEmit(e) {
					pending = append(pending, e)
					if len(pending) >= flushBatchSize {
						if !flush() {
							break readLoop
						}
					}
				} else {
					// e was already delivered in a prior poll (that is why it dedups). Carry
					// its identity forward so the committed boundary second keeps deduping it
					// once new same-second events advance the batch; otherwise the dedup set
					// would shrink to only this poll's new arrivals and re-emit e next time.
					r.cursor.recordDelivered([]*logEvent{e})
				}
			}
		}
		if rerr != nil {
			if rerr != io.EOF {
				scanErr = rerr
			}
			break
		}
	}

	if consumeErr != nil {
		cancel() // stop the log subprocess so wait() returns promptly
	}
	stderr, werr := wait()
	if consumeErr == nil {
		flush() // the final buffered second
	}

	// Fold the delivered seconds into the committed cursor and persist. batchSecond reflects
	// only delivered data, so this is safe to run even after a consume error — it advances
	// exactly to the last second that was accepted.
	r.cursor.commit()
	if r.storage != nil {
		if data, merr := r.cursor.marshal(); merr == nil {
			if serr := r.storage.Set(ctx, cursorStorageKey, data); serr != nil {
				r.logger.Warn("failed to persist cursor", zap.Error(serr))
			}
		}
	}

	if consumeErr != nil {
		return emitted, fmt.Errorf("consume failed: %w", consumeErr)
	}
	if scanErr != nil {
		return emitted, fmt.Errorf("error reading log output: %w", scanErr)
	}
	if werr != nil && ctx.Err() == nil {
		return emitted, fmt.Errorf("log exited with error: %w (stderr: %s)", werr, stderr)
	}
	return emitted, nil
}

func newBatch() (plog.Logs, plog.LogRecordSlice) {
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	sl := rl.ScopeLogs().AppendEmpty()
	return logs, sl.LogRecords()
}

// nextLine reads one newline-terminated line from br. A line that fills the reader buffer
// without a newline (i.e. longer than the buffer's byte cap) is discarded up to the next
// newline and reported via oversized>0 with a nil line, so the caller can skip it instead of
// stalling. The returned line aliases br's buffer and is valid only until the next read.
func nextLine(br *bufio.Reader) (line []byte, oversized int, err error) {
	line, err = br.ReadSlice('\n')
	if err != bufio.ErrBufferFull {
		return line, 0, err
	}
	oversized = len(line)
	for err == bufio.ErrBufferFull {
		var chunk []byte
		chunk, err = br.ReadSlice('\n')
		oversized += len(chunk)
	}
	return nil, oversized, err
}

// readFromArchive runs one log invocation per resolved archive path (one-shot, no cursor).
func (r *unifiedLoggingReceiver) readFromArchive(ctx context.Context) {
	for _, path := range r.cfg.resolvedArchivePaths {
		args := []string{"show", "--archive", path, "--style", "ndjson"}
		// start_time/end_time are interpreted as UTC (the +0000 suffix), matching live
		// mode's cursor handling, so the same wall-clock string means the same instant
		// regardless of the host's local timezone.
		if r.cfg.StartTime != "" {
			args = append(args, "--start", r.cfg.StartTime+"+0000")
		}
		if r.cfg.EndTime != "" {
			args = append(args, "--end", r.cfg.EndTime+"+0000")
		}
		if r.cfg.Predicate != "" {
			args = append(args, "--predicate", r.cfg.Predicate)
		}
		stdout, wait, err := r.runner.Run(ctx, args)
		if err != nil {
			r.logger.Error("failed to start log for archive", zap.String("archive", path), zap.Error(err))
			continue
		}
		logs, records := newBatch()
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 10*1024*1024)
		for scanner.Scan() {
			if ctx.Err() != nil {
				break
			}
			e, perr := parseLogEvent(scanner.Bytes())
			if perr != nil || e == nil {
				continue
			}
			e.setLogRecord(records.AppendEmpty(), r.now())
			if records.Len() >= flushBatchSize {
				_ = r.consumer.ConsumeLogs(ctx, logs)
				logs, records = newBatch()
			}
		}
		_, _ = wait()
		if records.Len() > 0 {
			_ = r.consumer.ConsumeLogs(ctx, logs)
		}
	}
}
