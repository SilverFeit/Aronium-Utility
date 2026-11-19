# Changelog

All notable changes to **Aronium Utility** are documented in this file.

## [1.1.0] — 2026-11-19

### Added
- **Aronium Utility UI (`aronium-utility-ui.exe` — Tauri 2 + Svelte 5 + TypeScript + Go CLI Sidecar Architecture):**
  - Lightweight desktop application (`ui/src/App.svelte`, `ui/src/lib/ipc.ts`, `ui/src-tauri/src/lib.rs`, `cmd/aronium-utility-ui/main.go`) that renders via the operating system's native WebView (zero bundled Chromium / Electron overhead) and consumes the existing Go CLI (`aronium-util.exe`) as its sidecar execution engine with **zero duplicated business logic**.
  - Launchable directly via the desktop executable **`aronium-utility-ui.exe`** or from the CLI via **`aronium-util ui`**.
  - Visual interactive panels for:
    1. **Live Dashboard & Metrics:** Consolidated sales KPIs, active customer credit portfolio, inventory valuation, stock alerts, sales by cash register bar visualization, and top-selling products.
    2. **Customer Credit Accounts Manager:** Visual clerk form to add annotations/payment commitments (`credit note add`) and credit limits (`credit limit`) with instant synchronization to Google Drive, plus expandable customer account histories and statements.
    3. **Live Inventory, Alerts & Kardex:** Searchable stock table with status filters (`OK`, `STOCK_BAJO`, `AGOTADO`, `STOCK_NEGATIVO`) and real-time Kardex movement viewer.
    4. **Cash Register Comparison (`POS-01`, `POS-02`):** Per-register transaction counts, cash vs. credit sales, and average ticket metrics.
    5. **Google Drive Web OAuth 2.0 & Hot Backups:** 1-click Google Drive web login, instant table sync, and encrypted (`AES-256-GCM`) hot SQLite backup generator.
    6. **System Health Doctor & Auto-Start Service Control:** Live `PRAGMA quick_check` diagnostics and Windows boot startup registration.
- **Automatic Startup on PC Boot (`aronium-util.exe watch`):**
  - The unified installer automatically registers `aronium-util.exe watch` in Windows Startup (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` and `schtasks`) so the CLI runs as soon as the computer turns on and operates on live transactions in `pos.db`.
- **Unified Single Installation (`aronium-util-setup-x64.exe` & `aronium-util-setup-x86.exe`):**
  - Embeds and installs **both** `aronium-util.exe` (CLI Engine) and `aronium-utility-ui.exe` (Desktop UI), configures `PATH` and `ARONIUM_*` environment variables, registers the startup watcher, and creates a Desktop shortcut for **Aronium Utility UI**.

### Changed
- **Universal International Terminology:** Replaced regional colloquialisms with standard business terminology across the CLI, UI, database tables, and Excel/Google Drive workbooks (`credit` / `accounts`, `Cuentas de Crédito por Cliente`, `Aronium_Inventario_y_Cuentas_Credito.xlsx`, `fiscal` UBL 2.1 electronic invoicing).

---

## [1.0.0] — 2026-11-18

### Added
- **Clean-Room 100% Pure Go CLI Engine (`aronium-util.exe`)** released under the **MIT License**.
- **Strict Read-Only SQLite Engine (`mode=ro`) + Independent State DB (`aronium_utility_state.db`).**
- **Direct Web OAuth 2.0 Authentication with Google Drive & Google Sheets (`aronium-util drive login`).**
- **Unified Inventory + Customer Credit Account Routing to Google Drive & Sheets.**
- **Multi-Station Central Hub (`POS-01`, `POS-02`), Hot SQLite Online Backups (`VACUUM INTO`), Multi-Engine Migration (`SQL Server Express` / `PostgreSQL`), and Modular UBL 2.1 Fiscal Connector.**
