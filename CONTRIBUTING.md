# Contributing to Aronium Utility

Thank you for your interest in contributing to **Aronium Utility**!

## Clean-Room & MIT License Policy

**Aronium Utility** is an independent, clean-room open-source CLI and synchronization suite licensed under the [MIT License](LICENSE).
- It reads the local SQLite database (`pos.db`) produced by Aronium Lite in read-only mode (`mode=ro`) and maintains its own extended state in `aronium_utility_state.db`.
- It does not modify proprietary binaries, evade licensing systems, or replace official Aronium Pro support.

## Development & Cross-Compilation Workflow

Requires Go 1.22+ (pure-Go SQLite via `modernc.org/sqlite` — zero CGO dependencies):

```bash
# Run the complete unit, BDD (Gherkin) & QA Matrix (TC-001..TC-030) test suite
go test -v ./...

# Build Windows x64, Windows x86, Windows Setup Installer (.exe) & Release packages
./Scripts/package_release.sh
```

## Pull Request Checklist

- [ ] All unit tests, concurrency stress tests, and BDD scenarios pass (`go test ./...`).
- [ ] `pos.db` queries preserve strict `mode=ro` semantics.
- [ ] Sensitive customer fields (tax IDs, phones, emails) pass through `config.RedactPII` in logs.
- [ ] Windows `amd64` and `386` cross-compilation succeeds with `CGO_ENABLED=0`.
