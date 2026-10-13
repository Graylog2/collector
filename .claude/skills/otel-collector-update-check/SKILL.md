---
name: otel-collector-update-check
description: Check OpenTelemetry Collector Updates
disable-model-invocation: true
---

# Check OpenTelemetry Collector Updates

Summarize what changes for the Graylog Collector (this repository) between the OpenTelemetry Collector versions it uses and the latest releases of:

- https://github.com/open-telemetry/opentelemetry-collector/releases (core)
- https://github.com/open-telemetry/opentelemetry-collector-contrib/releases (contrib)

Changelogs alone miss breakage from transitive dependencies and from upstream code we copied. Every run therefore includes a trial bump and a fork check.

## Version sources

Read the current versions from all of these; they can drift apart:

| File | What it pins |
|---|---|
| `builder/builder-config.yaml` | Components (v0.x) and confmap providers (v1.x) |
| `go.mod` (root) | `go.opentelemetry.io/collector/cmd/builder` tool |
| `builder/go.mod` | Generated from the two files above; resolved versions of everything the binary links |
| `receiver/*/go.mod`, `extension/*/go.mod` | Per-module OTel deps and `go` directive |
| `superv/go.mod` | `github.com/open-telemetry/opamp-go` |

Releases carry two version tracks (for example `v1.68.0/v0.162.0`): stable modules such as `pdata`, `confmap` and `component` use `v1.N.0`, the rest use `v0.M.0`. Keep each module on its own track. Git tags, changelog URLs and `cmd/builder` use `v0.M.0`.

## Steps

1. **Determine the exact old and new versions** from the version sources and the release lists. Get the release list with `curl -s 'https://api.github.com/repos/open-telemetry/<repo>/releases?per_page=30'` (`gh` may be unauthenticated) and confirm it reaches back to the old version.
2. **Read every changelog in the range.** For each repository, read both `CHANGELOG.md` and `CHANGELOG-API.md` from `https://raw.githubusercontent.com/open-telemetry/<repo>/<new-tag>/<file>`, every `## ` section after the old version up to and including the new one. API removals often appear only in `CHANGELOG-API.md`. Don't rely on PR descriptions or release pages; they may be truncated.
3. **Filter by component name, not module name.** Changelog entries use snake_case component IDs, which don't always match the module path: `healthcheckextension` → `extension/health_check`, `storage/filestorage` → `extension/file_storage`. Derive the ID for every entry in `builder/builder-config.yaml`. Also read every entry for `all`, `pkg/service`, `pkg/confighttp`, `pkg/confmap`, `pkg/exporterhelper`, `pkg/stanza`, `pkg/ottl`, `pkg/pdata`, and every 🛑 Breaking and 🚩 Deprecations section in full. Pay attention to:
   - Breaking changes, removals, deprecations, changed defaults or behavior, feature-gate stage changes
   - API, configuration, serialization, protocol and persistence compatibility
   - Go and runtime requirements
   - Security fixes that change how a component should be used
4. **Trial-bump in scratch copies.** Never modify the working tree. Create two copies of the committed code with `git archive HEAD | tar -x -C <dir>`: `<scratch>/base` and `<scratch>/bump`.
   - **Baseline (`base`):** run the build and tests below, record the failures, and save `go list -m all` for every module listed under Version sources.
   - **Bump (`bump`):** set the new versions in `builder/builder-config.yaml`, run `go get go.opentelemetry.io/collector/cmd/builder@<v0 tag>` in the root, then `go generate .` to regenerate `builder/` (it also runs `go tool addlicense`; a failure there is a tooling issue, not a finding). In every other module, `go get` each direct `go.opentelemetry.io/collector/...` and `github.com/open-telemetry/opentelemetry-collector-contrib/...` requirement at the new version of its track, then run `go mod tidy`. In `superv/`, set `github.com/open-telemetry/opamp-go` to the version `builder/go.mod` now resolves, since the binary links that version.
   - **Build:** in `builder/`, `CGO_ENABLED=0 GOOS=<os> go build -o /dev/null .` for `linux`, `windows`, `darwin`.
   - **Test:** in every module listed under Version sources, `go test ./...` and `GOOS=windows go vet ./...`.
   - Don't run `go fix` in the trial; it rewrites unrelated files.
   - A failure in `bump` that is absent from `base` is a finding: trace it to the upstream change or transitive dependency (step 5) that caused it. Failures in both are pre-existing.
5. **Diff transitive dependencies.** Compare `go list -m all` of every module between `base` and `bump`. Check every module whose version changes (any change for v0 modules, minor or major for v1+) and that repository code imports directly (`grep -rn '"<module path>' --include=*.go`). For each, read its release notes for the range and diff its exported API: `go mod download -json <module>@<version>` gives each version's `Dir`; run `go doc -all ./<pkg>` there. If `go doc` fails for missing dependencies, compare the exported `func` and `type` declarations with `grep` instead. Always check `github.com/open-telemetry/opamp-go`, `go.opentelemetry.io/otel/*`, `google.golang.org/grpc`. For opamp-go, also read the opamp-spec releases its notes reference ("Update to opamp-spec …").
6. **Check forked upstream code.** Usage searches don't find code we copied. Compare upstream fixes in the range against:
   - `receiver/windowseventlogreceiver/internal/windows` ← contrib `pkg/stanza/operator/input/windows`
   - `superv` ← contrib `cmd/opampsupervisor` (our own supervisor: upstream supervisor changes don't apply, but their bug fixes can point to the same bug in ours)
7. **Check our customizations.** The repository doesn't set global OTel handlers itself, so upstream changes to them change what our code runs under. Compare upstream changes to logging, internal telemetry, global OTel handlers (`otel.SetErrorHandler`, propagators), `otelcol.CollectorSettings` and `confighttp` defaults against `builder/main_customize.go`, `builder/main_windows_customize.go`, `superv/ownlogs`, and the collector config `superv` injects (`superv/configmerge/inject.go`).
8. **Map each relevant change to concrete repository code.** Trace imports, API calls, implementations, configuration, tests and shared interfaces. For each change, name the affected files and symbols, explain why the usage is compatible or at risk, and name the required migration or test coverage. When an upstream breaking change does not affect us, say so and cite the search that shows it.
9. **Check the Graylog server** when a finding involves collector config the server generates (component names, config keys, defaults), OpAMP protocol or protobuf changes, or connection settings and own-logs offers. Clone it into the scratch directory with `git clone --depth 1 https://github.com/Graylog2/graylog2-server.git` and search:
   - `graylog2-server/src/main/java/org/graylog/collectors/config/`: the collector config the server generates (receivers, processors, exporters, extensions)
   - `graylog2-server/src/main/java/org/graylog/collectors/opamp/` (mainly `OpAmpService.java`): OpAMP message handling, offers, status reading
   - `graylog2-server/src/main/proto/opamp/proto/`: the server's own copy of the OpAMP protobufs, which can lag behind opamp-go

   Use the result to move findings from needs verification to confirmed or Not affected. Report server-side changes that are needed under Action required, naming the server repository.

## Report

Cite links to upstream sources (release, changelog entry or PR) and repository paths with line numbers. Mark every finding as **confirmed** or **needs verification**, and keep it in its topic section either way:
- **Confirmed:** shown by a build or test failure, or our code uses the exact symbol, config key or default that changed.
- **Needs verification:** the impact depends on runtime behavior that no build or test exercised. Say what would confirm it.

Report only changes to components, packages and dependencies we use. Breaking changes among those that don't affect us go under Not affected.

The report has these sections, in order:

1. **Version range**: old → new for core, contrib, and changed transitive dependencies.
2. **Action required**: ordered by severity. State `No actionable findings.` when empty.
3. **Housekeeping**: version bumps needed beyond the components (builder tool, providers, per-module `go.mod`, `go` directives). Items here aren't repeated under Action required.
4. **Behavior changes, no code change needed.**
5. **Not affected**: upstream breaking changes, fork fixes and customization-relevant changes that don't affect us, each with the evidence.
6. **Pre-existing failures**: failures from the baseline run.
7. **Not verified**: checks that couldn't be run and why (for example, tests that only run on Windows or macOS).
