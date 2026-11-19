# Security Policy & Data Privacy (`SECURITY.md`)

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.0.x   | :white_check_mark: |
| < 1.0   | :x:                |

## Core Security & Privacy Principles

**Aronium Utility** handles sensitive commercial, credit ("Fiados"), inventory, and tax (DIAN) records for small and medium retail stores. To protect merchants and their customers:

1. **Strict Read-Only POS Database Access (`mode=ro`):**
   - `aronium-util` opens `%LOCALAPPDATA%\Aronium\Data\pos.db` in strict read-only mode (`mode=ro&_busy_timeout=5000`).
   - It never modifies proprietary binaries, bypasses licenses, or writes unchecked transactions directly into a live `pos.db`.
2. **Zero Plaintext Secrets (`TODO-064`):**
   - Google OAuth 2.0 tokens (`access_token` / `refresh_token`), DIAN technological provider API keys, Hub tokens, and SMTP passwords are encrypted at rest using **AES-256-GCM** bound to machine/user secret storage (Windows Credential Manager / DPAPI vault) and are never emitted in logs or committed to version control.
3. **Personal Data Protection & Habeas Data Compliance (Colombia Ley 1581 de 2012 — `TODO-065`):**
   - All diagnostic and debug log streams automatically mask Personally Identifiable Information (PII), including customer tax IDs (`CC`/`NIT`), phone numbers, and email addresses.
4. **Encrypted Hot Backups (`TODO-039`):**
   - Hot SQLite backups created via `aronium-util backup create` support **AES-256-GCM** authenticated encryption and **SHA-256** integrity verification before upload to Google Drive or S3.
5. **Authenticated Multi-Station LAN / Hub Communications (`NFR-007`, `TODO-066`):**
   - Communication between POS workstations (`POS-01`, `POS-02`) and `aronium-util hub` requires API key / bearer token authentication and supports TLS transport.

## Reporting a Vulnerability

If you discover a security vulnerability, credential leak vector, or fiscal/DIAN payload issue:
1. **Do NOT open a public GitHub issue with live customer `pos.db` dumps or OAuth tokens.**
2. Report the finding privately via GitHub Security Advisories on [`SilverFeit/Aronium-Utility`](https://github.com/SilverFeit/Aronium-Utility/security).
3. Include steps to reproduce using synthetic fixtures generated via `aronium-util test-fixture`.
