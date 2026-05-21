# slope sandbox

Minimal CTF-focused sandbox service for Windows samples on a Linux KVM/libvirt host.

I chose Go because the service is mostly control-plane code: HTTP, SQLite state, filesystem artifacts, timeouts, and libvirt calls. Go keeps those pieces explicit with a small dependency set and simple test doubles.

## Architecture

- API service: `POST /tasks`, `GET /tasks/{id}`, `GET /tasks/{id}/screenshots`, `GET /tasks/{id}/artifacts/{name}`.
- Scheduler/worker: claims pending tasks, locks one compatible Windows machine, runs the lifecycle, and always releases the lock.
- Libvirt VM controller: connects only to `qemu:///system`, restores the configured snapshot, starts the domain, captures host-side screenshots, and destroys the domain at teardown.
- Guest runner: minimal HTTP command channel. It can upload a host-path sample to the guest, execute it, and poll `GET /run/{id}` for `running|completed|failed`.
- Artifact store: writes `storage/tasks/<task_id>/meta.json`, screenshot files such as `shots/0001.png`, and `run.log`.
- State store: SQLite with `tasks`, `machines`, `task_events`, and `screenshots`.

## Prerequisites

Ubuntu/Debian host:

```bash
sudo apt update
sudo apt install -y qemu-kvm libvirt-daemon-system libvirt-clients virtinst pkg-config libvirt-dev
sudo usermod -aG libvirt,kvm "$USER"
```

Log out and back in, then verify:

```bash
virsh -c qemu:///system list --all
pkg-config --cflags --libs libvirt libvirt-admin
```

Create a Windows VM, install the guest agent inside it, and create a clean snapshot:

```bash
virsh -c qemu:///system snapshot-create-as win-ctf-01 clean
```

## Build And Run

The production binary must be built with the `libvirt` tag:

```bash
go build -tags libvirt -o slope-sandbox ./cmd/slope-sandbox
./slope-sandbox -config config.example.yaml
```

Without the tag, the code still builds and tests with a startup stub, but real VM control is disabled.

## Systemd Install

On a libvirt host, install the service with:

```bash
sudo apt install -y golang pkg-config libvirt-dev
./scripts/install-systemd.sh
```

The installer builds `cmd/slope-sandbox` with `-tags libvirt`, installs it to `/usr/local/bin/slope-sandbox`, installs config at `/etc/slope/config.yaml`, creates `/var/lib/slope`, writes `/etc/systemd/system/slope-sandbox.service`, then enables and starts it.

To install using your own config:

```bash
CONFIG_SRC=config.smoketest.yaml ./scripts/install-systemd.sh
```

Existing `/etc/slope/config.yaml` is kept by default. To replace it:

```bash
CONFIG_SRC=config.smoketest.yaml OVERWRITE_CONFIG=1 ./scripts/install-systemd.sh
```

After editing VM names or snapshots:

```bash
sudo systemctl restart slope-sandbox.service
sudo systemctl status slope-sandbox.service
journalctl -u slope-sandbox.service -f
```

The service runs as the `slope` system user and is added to `libvirt`/`kvm` groups when those groups exist. Place submitted host-side samples somewhere that this user can read, such as a dedicated directory under `/var/lib/slope`.

Build the Windows guest agent. The default project build produces a Windows GUI-subsystem binary so no console window is created:

```bash
make build-guest-agent
```

Equivalent direct command:

```bash
GOOS=windows GOARCH=amd64 go build \
  -trimpath \
  -ldflags="-s -w -H windowsgui" \
  -o build/slope-guest-agent.exe ./cmd/slope-guest-agent
```

A plain `go build ./cmd/slope-guest-agent` creates a console-subsystem binary and is only recommended for interactive debugging.

Put `slope.conf` next to `slope-guest-agent.exe`:

```json
{
  "addr": "0.0.0.0:9000",
  "root": "C:\\slope",
  "allowed_roots": [
    "C:\\slope",
    "C:\\Windows\\System32"
  ],
  "max_bytes": 536870912
}
```

Run it inside the Windows VM, normally before taking the clean snapshot:

```powershell
C:\slope\slope-guest-agent.exe
```

The agent defaults to loading `slope.conf` from the same directory as the executable. CLI flags such as `-config`, `-addr`, `-root`, and `-allow-roots` still exist as overrides for testing.

The guest firewall must allow inbound TCP 9000:

```powershell
New-NetFirewallRule `
  -DisplayName "Slope Guest Agent" `
  -Direction Inbound `
  -Action Allow `
  -Protocol TCP `
  -LocalPort 9000
```

For CTF VMs that include the guest agent by default, `guest_endpoint` may be left empty. The service will try libvirt address discovery in this order: DHCP lease, QEMU guest agent, then ARP. After an endpoint is known, the worker waits for the guest agent `/health` endpoint for `guest_ready_timeout_ms` before uploading files. If discovery is unreliable for a VM, set `guest_endpoint` explicitly in the machine profile.

## API Examples

Submit a task:

```bash
curl -sS -X POST http://127.0.0.1:8080/tasks \
  -H 'Content-Type: application/json' \
  -d '{"sample_ref":"/srv/samples/chal.exe","vm_name":"win-ctf-01","timeout_sec":60}'
```

If `sample_ref` is a host file path, the worker uploads it to the guest agent under `samples/<basename>` and executes that guest-side path. Hash-like references are passed through as-is.

Check status:

```bash
curl -sS http://127.0.0.1:8080/tasks/<task_id>
```

List screenshots:

```bash
curl -sS http://127.0.0.1:8080/tasks/<task_id>/screenshots
```

Fetch an artifact:

```bash
curl -o final.png http://127.0.0.1:8080/tasks/<task_id>/artifacts/shots/0001.png
```

## Visible Sample Smoke Test

Build a small Windows binary that visibly proves execution:

```bash
zig cc -target x86_64-windows-gnu -O2 \
  examples/visible_sample.c \
  -Wl,--subsystem,windows \
  -o build/visible_sample_gui.exe
```

Use a config like `config.smoketest.yaml` with a Windows VM snapshot that already contains the guest agent. Leave `guest_endpoint` unset to use libvirt address discovery:

```yaml
guest_ready_timeout_ms: 10000

machines:
  - name: "win-ctf-01"
    platform: "windows"
    snapshot: "clean-agent"
    guest_port: 9000
```

Run the service and submit through the service API:

```bash
go run -tags libvirt ./cmd/slope-sandbox -config config.smoketest.yaml

curl -sS -X POST http://127.0.0.1:18080/tasks \
  -H 'Content-Type: application/json' \
  -d '{"sample_ref":"/path/to/slope/build/visible_sample_gui.exe","vm_name":"win-ctf-01","timeout_sec":30}'
```

This test should complete after the configured run window even though the MessageBox stays open. Screenshots should show the dialog, and the VM should still be stopped and released.

## Tests

Run portable tests:

```bash
go test ./...
```

Verify the libvirt-backed build path on a host with headers:

```bash
go test -tags libvirt ./...
go build -tags libvirt ./cmd/slope-sandbox
```

## Safety Notes

- Startup validation rejects any backend type except `libvirt`.
- Config validation rejects every non-Windows machine profile.
- Task submission enforces timeout and sample-size limits.
- Artifact serving is restricted to the task directory.
- Screenshot failures are logged as task events and do not fail the task by themselves.
