.PHONY: test build-sandbox build-guest-agent build-visible-sample install-systemd

test:
	go test ./...
	go test -tags libvirt ./...

build-sandbox:
	go build -tags libvirt -o build/slope-sandbox ./cmd/slope-sandbox

build-guest-agent:
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H windowsgui" -o build/slope-guest-agent.exe ./cmd/slope-guest-agent

build-visible-sample:
	zig cc -target x86_64-windows-gnu -O2 examples/visible_sample.c -Wl,--subsystem,windows -o build/visible_sample_gui.exe

install-systemd:
	./scripts/install-systemd.sh
