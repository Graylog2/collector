# Download-page install scripts

These are the per-platform "one-liner" install scripts that the **Graylog
server** serves on its collector download page. They are tracked here as the
source of truth, but they are **reference copies only**:

- They are **not** included in any build artifact (`.pkg`, `.msi`, `.deb`,
  `.rpm`). No packaging task copies this directory — the macOS `prepare` task
  only sweeps `dist/macos/`, WiX harvests components explicitly, and `nfpm.yaml`
  uses an explicit `contents:` allow-list.
- The download URLs are **placeholders**. The real download-service routes are
  not finalized yet; update them (and ideally template them server-side) before
  the server renders these.

| File | Platform | Installer it drives |
|------|----------|---------------------|
| `install.ps1` | Windows | MSI (`msiexec` with `ENROLLTOKEN` / `ENROLLENDPOINT` properties) |
| `install.sh`  | macOS   | pkg (pre-seeds `supervisor.yaml`, then `installer -pkg`) |

Both take the enrollment endpoint and token as input and verify the service is
running after install.
