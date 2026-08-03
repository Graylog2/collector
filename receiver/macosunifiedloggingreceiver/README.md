# macOS Unified Logging Receiver

The macOS Unified Logging Receiver collects logs from the live macOS system log using the native
`log` command. Reading archived log files (`.logarchive`) is not supported.

| Status                |                     |
| --------------------- | ------------------- |
| Stability             | [alpha]: logs       |
| Supported Platforms   | darwin (macOS) only |
| Component type        | `macos_unified_logging` (deprecated alias: `macosunifiedlogging`) |

[alpha]: https://github.com/open-telemetry/opentelemetry-collector/blob/main/docs/component-stability.md#alpha

## Fork Origin

- **Upstream:** `receiver/macosunifiedloggingreceiver/`
- **Commit:** [`42f9491`](https://github.com/open-telemetry/opentelemetry-collector-contrib/commit/42f949127580c0d00088b785cd0b35842dc0ddb8)
- **Version:** v0.153.0

## Changes from Upstream

- The receiver maintains a forward cursor using `(machTimestamp, threadID)` deduplication, so
  events at the boundary second are emitted exactly once instead of being re-emitted on each
  poll. The `--start` value is always floored to whole seconds because the `log` command
  rejects fractional seconds, and is anchored to UTC (a `+0000` offset) so that a change in
  the host's local timezone or a DST transition cannot shift the cursor or skip/duplicate a
  window.
- The receiver always invokes `log show --style ndjson` internally. Each log record's body is
  set to the human-readable `eventMessage` field; structured `macos.*` attributes are emitted
  for every other field of interest. The user-visible `format` option is currently inert
  (reserved; not honored).
- Archive mode (`.logarchive` files) has been removed; the receiver reads the live system log
  only.
- The cursor is persisted via a collector storage extension (`storage:` is required). On restart the cursor is restored and polling resumes from where it left off. A
  `bootUUID` change (reboot detected mid-stream) resets the cursor automatically.
- The `log` binary is invoked at its fixed absolute path `/usr/bin/log` and integrity-verified
  at startup: filesystem ownership and SIP-restriction checks are required; an Apple
  code-signature check (`codesign --verify`) is performed as a best-effort second layer.
- A `min_poll_interval` (default `1s`, minimum `100ms`) floors the backoff. Both the default
  and the minimum exist to avoid a self-feeding poll loop, since `log show` logs its own
  invocations.
- An ndjson line longer than 10 MB is skipped with a diagnostic rather than stalling the
  reader. Upstream used a `bufio.Scanner` with the same 10 MB cap but no skip path, so an
  oversized line ended the read silently.
- The predicate is no longer validated in-process. `log` owns its grammar; the previous
  allowlist/blocklist rejected valid filters, accepted invalid ones, and rewrote string
  literals. See [Predicate Handling](#predicate-handling).

## Requirements

- macOS 10.12 (Sierra) or later
- The `log` command must be available at `/usr/bin/log` (standard macOS location)
- Appropriate permissions to read system logs
- A storage extension configured in the collector pipeline (see `storage:` below)

## Configuration

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `storage` | string | — | Component ID of a storage extension used to persist the read cursor (e.g., `file_storage/default`). **Required** — config validation fails without it, so `otelcol validate` catches it offline. |
| `predicate` | string | `""` | Filter predicate (e.g., `"subsystem == 'com.apple.example'"`) |
| `start_time` | string | `""` | Where to begin reading on a **cold start** (no persisted cursor), in format `"2006-01-02 15:04:05"`, interpreted as **UTC**. Takes precedence over `max_log_age`. Ignored once a cursor exists. |
| `max_log_age` | duration | `24h` | On a **cold start** with no `start_time`, begin reading this far back. Ignored once a cursor exists, except to warn when the cursor is older (see [Live Cursor](#live-cursor)). |
| `min_poll_interval` | duration | `1s` | Minimum (floor) poll interval. Must be at least `100ms`; lower values are rejected because `log show` logs its own invocations and a shorter floor lets the receiver's own reads sustain a self-feeding poll loop. |
| `max_poll_interval` | duration | `30s` | Maximum interval between polls. Uses exponential backoff starting from `min_poll_interval`. |
| `format` | string | `"default"` | **Currently has no effect (reserved).** Accepts `default`, `ndjson`, `json`, `syslog`, or `compact`, but the receiver always reads `ndjson` internally, so this option is not honored. |

### Exponential Backoff Behavior

The receiver uses exponential backoff to optimize polling based on log activity:

- **Active logging**: When new events are emitted, the poll interval resets to `min_poll_interval` (default `1s`).
- **Idle period**: When no new events are found, the interval doubles on each poll up to `max_poll_interval` (default `30s`).
- **Automatic reset**: As soon as events are detected again, the interval returns to `min_poll_interval`.

This minimizes both latency during active logging and resource usage during idle periods.

> **Known limitation.** `log show` writes a log record for its own invocation, so a
> receiver configured without a `predicate` sees at least one "new" event on every poll and
> the interval stays pinned at `min_poll_interval` — the idle backoff above effectively never
> engages. A `predicate` that excludes the receiver's own reads restores it.

### Basic Configuration

The receiver requires a storage extension. A minimal pipeline configuration:

```yaml
extensions:
  file_storage/default:
    directory: /var/lib/otelcol/storage

receivers:
  macos_unified_logging:
    storage: file_storage/default   # required
    max_poll_interval: 30s
    max_log_age: 24h

service:
  extensions: [file_storage/default]
  pipelines:
    logs:
      receivers: [macos_unified_logging]
      exporters: [...]
```

### With Filtering

```yaml
receivers:
  macos_unified_logging:
    storage: file_storage/default
    predicate: "subsystem == 'com.apple.systempreferences'"
```

### Backfilling from a Fixed Point

On a cold start (no persisted cursor) `start_time` overrides the `max_log_age` default:

```yaml
receivers:
  macos_unified_logging:
    storage: file_storage/default
    start_time: "2024-01-01 00:00:00"   # UTC; ignored once a cursor is persisted
```

## Predicate Examples

Filter by subsystem:
```
subsystem == 'com.apple.systempreferences'
```

Filter by process:
```
process == 'kernel'
```

Filter by message type:
```
messageType == 'Error'
```

Combine filters:
```
subsystem == 'com.apple.example' AND messageType IN {'Error', 'Fault'}
```

For a full description of predicate expressions, run `log help predicates`. Note that the field
list printed there is not exhaustive — `log` also accepts ndjson field names such as
`messageType` and `eventMessage`.

### Predicate Handling

The predicate is passed to `log` exactly as configured. The receiver does not parse, validate or
rewrite it; `log` has the real NSPredicate parser and is the only authority on what it accepts. A
malformed predicate therefore surfaces on the first poll, as an error carrying `log`'s own
diagnostic:

```
log poll failed: log exited with error: exit status 64
  (stderr: log: Bad predicate (Unable to parse the format string "subsystem =="): subsystem ==)
```

The receiver keeps polling and keeps reporting the error; it does not fall back to reading
unfiltered, which would replace a filtered stream with the entire system log.

**Why unvalidated input is safe here.** The predicate is untrusted: it arrives via collector
config, which may be pushed from a central fleet manager by operators without shell access to the
host. Two properties of the invocation contain it:

- `log` is executed directly via `exec`, never through a shell, so `;`, `|`, `$`, backticks and
  redirects in a predicate are inert bytes with no interpretation.
- The predicate is a single `argv` element, so it cannot introduce additional flags — it can
  filter what is read, never change what is read.

A predicate cannot widen access: the receiver already reads the whole system log, and filtering
only narrows that.

Earlier versions rejected predicates containing shell metacharacters. That check was removed: it
prevented nothing (neither property above depends on it), while making legitimate filters
impossible — any message containing `$`, `;`, `|` or a backtick was unsearchable — and it
silently rewrote `&&` to `AND` inside string literals, changing what the filter matched.

## Output Format

The receiver always invokes `log show --style ndjson` internally, regardless of the `format` setting. Each ndjson record is mapped to an OTel log record as follows:

- **Body**: The `eventMessage` field (human-readable message). Falls back to the raw JSON line if `eventMessage` is empty.
- **Timestamp**: Parsed from the `timestamp` field (format `2006-01-02 15:04:05.000000-0700`).
- **ObservedTimestamp**: Set to the time the record was processed.
- **SeverityText / SeverityNumber**: Mapped from `messageType`:
  - `"Error"` → `Error`
  - `"Fault"` → `Fatal`
  - `"Default"`, `"Info"` → `Info`
  - `"Debug"` → `Debug`

**Attributes** — the following `macos.*` attributes are set when the corresponding field is
present in the source record. An attribute you receive always came from `log`; absent fields
are omitted rather than defaulted. String fields are additionally omitted when empty, since
`""` carries no information. Integer fields distinguish absent from `0`, because `0` is a real
value — `userID: 0` is root and `processID: 0` is the kernel, and both are common in practice.

`macos.machTimestamp` and `macos.threadID` are the only integers always present: the receiver
rejects records without a `machTimestamp`, and the two together form the cursor's dedup key.

| Attribute | Type | Source field |
|-----------|------|-------------|
| `macos.subsystem` | string | `subsystem` |
| `macos.category` | string | `category` |
| `macos.eventType` | string | `eventType` |
| `macos.messageType` | string | `messageType` |
| `macos.processImagePath` | string | `processImagePath` |
| `macos.processImageUUID` | string | `processImageUUID` |
| `macos.senderImagePath` | string | `senderImagePath` |
| `macos.senderImageUUID` | string | `senderImageUUID` |
| `macos.formatString` | string | `formatString` |
| `macos.bootUUID` | string | `bootUUID` |
| `macos.processID` | int | `processID` |
| `macos.threadID` | int | `threadID` (always present) |
| `macos.machTimestamp` | int | `machTimestamp` (always present) |
| `macos.activityIdentifier` | int | `activityIdentifier` |
| `macos.parentActivityIdentifier` | int | `parentActivityIdentifier` |
| `macos.creatorActivityID` | int | `creatorActivityID` |
| `macos.traceID` | int | `traceID` |
| `macos.senderProgramCounter` | int | `senderProgramCounter` |

### Live Cursor

The receiver maintains a **forward cursor** so that each event is delivered exactly once:

1. **`--start` flooring**: The `log show --start` value must be a whole second (the `log`
   command rejects fractional seconds). The cursor records the wall-clock second of the
   latest event seen in each poll.
2. **Boundary-second deduplication**: Because `--start` is inclusive, events at the boundary
   second are re-fetched on the next poll. The cursor records the `(machTimestamp, threadID)`
   pair of every event at that boundary second and skips any that were already emitted.
3. **Persistence**: After a poll that advanced the cursor, it is serialized and saved via the
   storage extension identified by `storage:`. An idle poll leaves the cursor unchanged and is
   not rewritten, so a quiet system does not incur a storage write on every tick. On restart
   the cursor is loaded from storage and polling continues from the last committed position. If the stored cursor cannot be
   *read* at startup, the receiver fails to start rather than beginning fresh — a silent fresh
   start would re-read and re-ingest up to `max_log_age` of logs. A cursor that is present but
   unparseable, or that was written under a different `predicate`, is discarded with a log
   message and the receiver starts fresh (both are expected states, not failures).
4. **Reboot detection**: If the `bootUUID` field changes between events, the cursor is
   reset immediately — `machTimestamp` is monotonic per boot, so values from a previous boot
   are meaningless as a cursor position.

## Security

### `/usr/bin/log` Integrity Verification

At startup the receiver verifies that the `log` binary it is about to execute is the genuine Apple-supplied tool:

1. **Path check** (required): Only `/usr/bin/log` is accepted; any other path causes startup to fail.
2. **Filesystem / SIP check** (required, pure-Go): The file must be a regular (non-symlink) file, owned by `root:wheel`, not group- or world-writable, and marked with the `SF_RESTRICTED` flag (set by System Integrity Protection on the sealed system volume). A planted copy outside the system volume cannot carry this flag.
3. **Apple code-signature check** (best-effort): If `/usr/bin/codesign` itself passes the SIP check, the receiver runs `codesign --verify --strict` with the inline requirement `anchor apple and identifier "com.apple.log"`. If `codesign` is not available or does not pass its own SIP check, a warning is logged and startup proceeds relying on the filesystem checks alone.

### Untrusted Predicate Input

The predicate is untrusted, fleet-supplied input. It is contained structurally — direct `exec`
with no shell, and a single `argv` element that cannot introduce flags — rather than by filtering
its contents. See [Predicate Handling](#predicate-handling) above.

## Example

Complete example configuration:

```yaml
extensions:
  file_storage/default:
    directory: /var/lib/otelcol/storage

receivers:
  macos_unified_logging:
    storage: file_storage/default
    predicate: "subsystem BEGINSWITH 'com.apple'"
    max_log_age: 24h

exporters:
  file:
    path: "./output/logs.json"
    format: json

service:
  extensions: [file_storage/default]
  pipelines:
    logs:
      receivers: [macos_unified_logging]
      exporters: [file]
```
