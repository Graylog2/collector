// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package macosunifiedloggingreceiver // import "github.com/Graylog2/collector/receiver/macosunifiedloggingreceiver"

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
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
}

func newUnifiedLoggingReceiver(cfg *Config, set receiver.Settings, consumer consumer.Logs, runner logRunner) *unifiedLoggingReceiver {
	minI := cmp.Or(cfg.MinPollInterval, time.Second)
	maxI := cmp.Or(cfg.MaxPollInterval, 30*time.Second)
	return &unifiedLoggingReceiver{
		cfg:      cfg,
		id:       set.ID,
		logger:   set.Logger,
		consumer: consumer,
		runner:   runner,
		cursor:   newCursor(predicateHash(cfg.Predicate)),
		cadence:  newCadence(minI, maxI),
		now:      time.Now,
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
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.readLogs(ctx)
	}()
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

// startArgValue computes the next --start, capping re-reads at max_log_age.
func (r *unifiedLoggingReceiver) startArgValue() string {
	floor := r.now().Add(-r.cfg.MaxLogAge).Format(startLayout)
	cur := r.cursor.startArg()
	if cur == "" {
		if r.cfg.StartTime != "" {
			return r.cfg.StartTime
		}
		return floor
	}
	if cur < floor {
		return floor
	}
	return cur
}

func (r *unifiedLoggingReceiver) liveArgs(start string) []string {
	args := []string{"show", "--style", "ndjson", "--start", start}
	if r.cfg.Predicate != "" {
		args = append(args, "--predicate", r.cfg.Predicate)
	}
	return args
}

func (r *unifiedLoggingReceiver) pollOnce(ctx context.Context) (int, error) {
	stdout, wait, err := r.runner.Run(ctx, r.liveArgs(r.startArgValue()))
	if err != nil {
		return 0, fmt.Errorf("failed to start log: %w", err)
	}

	r.cursor.beginPoll()
	logs, records := newBatch()
	emitted := 0

	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		e, perr := parseLogEvent(scanner.Bytes())
		if perr != nil {
			r.logger.Warn("failed to parse log line", zap.Error(perr))
			continue
		}
		if e == nil {
			continue
		}
		if !r.cursor.shouldEmit(e) {
			continue
		}
		e.setLogRecord(records.AppendEmpty(), r.now())
		emitted++
		if records.Len() >= flushBatchSize {
			if cerr := r.consumer.ConsumeLogs(ctx, logs); cerr != nil {
				r.logger.Error("consume failed", zap.Error(cerr))
			}
			logs, records = newBatch()
		}
	}

	stderr, werr := wait()
	if records.Len() > 0 {
		if cerr := r.consumer.ConsumeLogs(ctx, logs); cerr != nil {
			r.logger.Error("consume failed", zap.Error(cerr))
		}
	}

	r.cursor.commit()
	if r.storage != nil {
		if data, merr := r.cursor.marshal(); merr == nil {
			if serr := r.storage.Set(ctx, cursorStorageKey, data); serr != nil {
				r.logger.Warn("failed to persist cursor", zap.Error(serr))
			}
		}
	}

	if serr := scanner.Err(); serr != nil {
		return emitted, fmt.Errorf("error reading log output: %w", serr)
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

// readFromArchive runs one log invocation per resolved archive path (one-shot, no cursor).
func (r *unifiedLoggingReceiver) readFromArchive(ctx context.Context) {
	for _, path := range r.cfg.resolvedArchivePaths {
		args := []string{"show", "--archive", path, "--style", "ndjson"}
		if r.cfg.StartTime != "" {
			args = append(args, "--start", r.cfg.StartTime)
		}
		if r.cfg.EndTime != "" {
			args = append(args, "--end", r.cfg.EndTime)
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
