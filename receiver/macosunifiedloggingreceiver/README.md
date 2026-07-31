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
- A `min_poll_interval` (default `1s`) floors the backoff. The `1s` default is chosen to avoid
  a self-feeding poll loop, since `log show` logs its own invocations.

## Requirements

- macOS 10.12 (Sierra) or later
- The `log` command must be available at `/usr/bin/log` (standard macOS location)
- Appropriate permissions to read system logs
- A storage extension configured in the collector pipeline (see `storage:` below)

## Configuration

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `storage` | string | — | Component ID of a storage extension used to persist the read cursor (e.g., `file_storage/default`). **Required** — startup fails without it. |
| `predicate` | string | `""` | Filter predicate (e.g., `"subsystem == 'com.apple.example'"`) |
| `start_time` | string | `""` | Where to begin reading on a **cold start** (no persisted cursor), in format `"2006-01-02 15:04:05"`, interpreted as **UTC**. Takes precedence over `max_log_age`. Ignored once a cursor exists. |
| `max_log_age` | duration | `24h` | On a **cold start** with no `start_time`, begin reading this far back. Ignored once a cursor exists, except to warn when the cursor is older (see [Live Cursor](#live-cursor)). |
| `min_poll_interval` | duration | `1s` | Minimum (floor) poll interval. Default `1s`, chosen to avoid a self-feeding poll loop (since `log show` logs its own invocations). |
| `max_poll_interval` | duration | `30s` | Maximum interval between polls. Uses exponential backoff starting from `min_poll_interval`. |
| `format` | string | `"default"` | **Currently has no effect (reserved).** Accepts `default`, `ndjson`, `json`, `syslog`, or `compact`, but the receiver always reads `ndjson` internally, so this option is not honored. |

### Exponential Backoff Behavior

The receiver uses exponential backoff to optimize polling based on log activity:

- **Active logging**: When new events are emitted, the poll interval resets to `min_poll_interval` (default `1s`).
- **Idle period**: When no new events are found, the interval doubles on each poll up to `max_poll_interval` (default `30s`).
- **Automatic reset**: As soon as events are detected again, the interval returns to `min_poll_interval`.

This minimizes both latency during active logging and resource usage during idle periods.

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

For a full description of predicate expressions, run `log help predicates`.

### Predicate Security

Predicate values are validated to ensure only valid predicate syntax is used. The following are not allowed:
- Command separators: `;`
- Pipes: `|` (`||` are normalized to `OR`)
- Variable expansion: `$`
- Backticks: `` ` ``
- Redirects: `>>`, `<<`
- Control characters: newlines, carriage returns

Valid predicate operators like `&&` (logical AND), `<`, `>` (comparison) are allowed. The `>` operator is allowed for comparisons (e.g., `processID > 100`) but blocked when followed by file paths. Note that `&&` is automatically normalized to `AND` for consistency. Use standard predicate syntax as documented by Apple's `log` command.

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

**Attributes** — the following `macos.*` attributes are set when the corresponding field is present (string fields are omitted when empty; integer fields are always set):

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
| `macos.threadID` | int | `threadID` |
| `macos.machTimestamp` | int | `machTimestamp` |
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
3. **Persistence**: After each successful poll the cursor is serialized and saved via the
   storage extension identified by `storage:`. On restart the cursor is loaded from storage
   and polling continues from the last committed position. If the stored cursor cannot be
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

### Predicate Injection Prevention

See [Predicate Security](#predicate-security) above.

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
