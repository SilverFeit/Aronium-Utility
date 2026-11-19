## Summary / Resumen

- **Related TODO / Issue:**
- **Module(s) Touched:** (`db`, `fiados`, `inventory`, `sync/gdrive`, `registers`, `hub`, `backup`, `migrate`, `reports`, `dian`, `installer`)

## Verification Checklist
- [ ] `go test -v ./...` passes all unit, BDD, and QA Matrix (`TC-001`..`TC-030`) tests
- [ ] Strict read-only (`mode=ro`) access to `pos.db` preserved
- [ ] Cross-compiled for Windows x64 (`GOOS=windows GOARCH=amd64`) and Windows x86 (`GOOS=windows GOARCH=386`)
- [ ] PII masking & secret vault checks verified (`TODO-064`, `TODO-065`)
