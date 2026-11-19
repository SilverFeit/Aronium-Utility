# Security Policy & Data Privacy (`SECURITY.md`)

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.1.x   | :white_check_mark: |
| 1.0.x   | :white_check_mark: |

## Core Security & Privacy Principles

**Aronium Utility** (Go CLI + Tauri 2 / Svelte Desktop UI) handles commercial, customer credit, inventory, and electronic invoicing records for retail businesses. To protect merchants and their customers:

1. **Strict Read-Only POS Database Access (`mode=ro`):**
   - `aronium-util` opens `%LOCALAPPDATA%\Aronium\Data\pos.db` in strict read-only mode (`mode=ro&_busy_timeout=5000`).
   - It never modifies proprietary binaries, bypasses licenses, or writes unchecked transactions directly into a live `pos.db`.
2. **Zero Plaintext Secrets (`TODO-064`):**
   - Google OAuth 2.0 tokens (`access_token` / `refresh_token`), fiscal provider API keys, Hub tokens, and SMTP passwords are encrypted at rest using **AES-256-GCM** bound to machine/user secret storage and are never emitted in logs or committed to version control.
3. **Personal Data Protection & PII Masking (`TODO-065`):**
   - All diagnostic and debug log streams automatically mask Personally Identifiable Information (PII), including customer tax IDs, phone numbers, and email addresses.
4. **Encrypted Hot Backups (`TODO-039`):**
   - Hot SQLite backups created via `aronium-util backup create` support **AES-256-GCM** authenticated encryption and **SHA-256** integrity verification before upload to Google Drive.
5. **Authenticated Multi-Station LAN / Hub Communications (`NFR-007`, `TODO-066`):**
   - Communication between POS workstations (`POS-01`, `POS-02`) and `aronium-util hub` requires API key / bearer token authentication and supports TLS transport.
