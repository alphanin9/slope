# CTF Scope

This service is intentionally narrow.

Included:

- Windows sample task submission by path or hash-like reference.
- Linux host with KVM/libvirt only.
- Libvirt snapshot revert before each run.
- Minimal guest execution trigger through an existing command channel.
- Periodic host-side screenshots and one final teardown screenshot attempt.
- SQLite state and local filesystem artifacts.

Out of scope:

- Static analysis.
- Behavioral parsing.
- Signature engines.
- YARA, Suricata, AV, or reputation integrations.
- Network packet capture or network analytics.
- Memory dumps.
- Process trees, registry diffs, filesystem diffs, or API tracing.
- Multi-hypervisor abstractions.
- Non-Windows guests.
- Heavy report generation.
