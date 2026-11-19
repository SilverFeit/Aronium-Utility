# Changelog

All notable changes to **Aronium Utility** are documented in this file.

## [1.0.0] — 2026-11-18

### Added
- **Clean-Room 100% Pure Go Architecture** (`cmd/aronium-util`, `cmd/aronium-util-setup`, `internal/config`, `internal/db`, `internal/events`, `internal/service`, `internal/fiados`, `internal/inventory`, `internal/sync`, `internal/registers`, `internal/hub`, `internal/admin`, `internal/backup`, `internal/migrate`, `internal/reports`, `internal/dian`, `internal/testutil`) released under the **MIT License**.
- **Strict Read-Only SQLite Engine (`mode=ro`) + Independent State DB (`aronium_utility_state.db`):**
  - Reads Aronium Lite's `%LOCALAPPDATA%\Aronium\Data\pos.db` with `PRAGMA query_only = ON`, `busy_timeout = 5000`, and exponential backoff retries (`db.WithRetry`) for zero `SQLITE_BUSY` contention.
  - Supports `ATTACH DATABASE` for read-only cross-queries against `pos.db` combined with read/write state tables.
- **Direct Web OAuth 2.0 Authentication with Google Drive & Google Sheets (`aronium-util drive login`):**
  - Launches a local web authentication portal (`http://127.0.0.1:8765`), opens the system browser automatically, exchanges OAuth 2.0 authorization codes for `access_token` + `refresh_token`, and persists credentials encrypted with **AES-256-GCM** (`gdrive.token.enc`).
- **Unified Inventory + Customer Credit ("Fiados") Routing to Google Drive & Sheets:**
  - Automatically routes any bill passed under a customer's name (`Customer.Name` / `Customer.Id` or note routing) with unpaid status (`PaidStatus = 0`), credit payment type (`"Fiado"`, `"Crédito"`, `"Cuenta Corriente"`), or split payments directly to that customer's credit ledger.
  - Generates the multi-sheet OpenXML workbook **`Aronium_Inventario_y_Fiados.xlsx`** (`Resumen_Fiados`, `Detalle_Fiados`, `Inventario`, and `Cliente_<Nombre>` tabs) plus individual customer files in `/AroniumUtility/Fiados/Clientes/<Nombre_Cliente>.xlsx`, `.csv`, and WhatsApp-ready `_Estado_Cuenta.txt`.
  - Synchronizes via **Google Drive API v3** (maintaining stable `file_id`s across updates) and **Google Sheets API v4** with offline-first outbox queueing (`sync_outbox`).
- **Native Windows Setup Installer (`aronium-util-setup-x64.exe` & `aronium-util-setup-x86.exe` + `install.bat` / `install.ps1`):**
  - Self-contained Windows installer executable that extracts `aronium-util.exe`, configures Windows User `PATH` and `ARONIUM_*` environment variables in `HKCU\Environment`, initializes `aronium-util.yaml`, and optionally launches the Google Drive web authentication flow.
- **Multi-Station Central Hub & Cash Register Identity (`POS-01`, `POS-02`):**
  - Duplicate register ID conflict detection (`TC-029`), sales filtering by terminal (`PRO-FR-004`), offline-first event queue reconciliation (`TC-015..016`, `TC-023..024`), simultaneous last-unit sale conflict auditing (`TC-021`), and concurrent catalog edit Last-Write-Wins auditing (`TC-022`).
- **Hot SQLite Online Backups (`VACUUM INTO`) & Multi-Engine Migration (`SQL Server Express` / `PostgreSQL`):**
  - Hot snapshots with ZIP compression, AES-256-GCM encryption, SHA-256 checksums, `PRAGMA integrity_check` verification, safe restore with pre-restore backup (`TC-017`, `TC-025`), and post-migration financial verifier (`MIG-004..006`).
- **Modular Colombia DIAN Electronic Invoicing Connector (`internal/dian`):**
  - UBL 2.1 / POS Electrónico payload builder, official SHA-384 `CUFE`/`CUDE` calculation, QR URL generation, XML/PDF artifact archiving to `/AroniumUtility/DIAN/YYYY-MM/`, Credit Notes, and offline contingency queue.
