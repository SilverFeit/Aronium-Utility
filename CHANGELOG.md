# Changelog

All notable changes to **Aronium Utility** are documented in this file.

## [1.2.0] — 2026-11-21

### Added
- **Dual-Database Architecture (`pos.db` Read-Only + `cuentas.db` WAL Ledger — Ruta A):**
  - Preserves Aronium Lite's daily cash register closing by isolating customer credit accounts, charges (`CARGO`), partial/full payments (`ABONO`), and adjustments (`AJUSTE`) inside a dedicated SQLite WAL database (`%LOCALAPPDATA%\AroniumUtility\cuentas.db`) created automatically on setup/init.
  - Strictly preserves `pos.db` as read-only (`mode=ro`) for live product catalog, multi-barcode joins (`GROUP_CONCAT`), and stock queries.
  - Freezes product names and integer unit prices (`movimiento_items`) at transaction time so later price edits in Aronium never alter existing customer balances.
- **Zero-Technical UI Experience (`Modo Básico` vs. `Modo Avanzado`) & Dedicated Account Builder:**
  - Top header mode toggle defaulting to **`Modo Básico`** for non-technical store clerks, with **`Modo Avanzado`** exposing system diagnostics, multi-station hub, and hot encrypted backups.
  - Automatic detection of `pos.db` and `cuentas.db`, friendly non-technical warning banner when `pos.db` is missing, and interactive OS file-picker modal (`aronium-util config pick-db`).
  - Dedicated **"Crear Cuenta / Anotar Artículos"** workspace capturing `id`, `nombre`, `telefono`, `metodo_pago`, `tope_credito`, and `descripcion`, plus optional HID barcode scanner field (`Enter` increments `+1` and retains focus) backed by an `mtime`-cached live catalog search from `pos.db`.
- **Net Inventory & 4-Tab Google Drive Workbook + Hot Ledger Backup:**
  - Real-time Net Stock computation (`Stock_Neto = Stock_Aronium - Unidades_Pendientes_En_Cuentas`) surfaced across the UI, CLI, and Google Drive reports.
  - Updated Master `.xlsx` & Google Sheets export with 4 consolidated sheets (`Cuentas`, `Movimientos`, `Detalle_Articulos`, `Inventario_Neto`), per-customer `.xlsx`/`.csv`/`.txt` statements, and `VACUUM INTO` hot backup (`cuentas_backup.db`).
- **Windows App Icon, PE Metadata, Startup Shortcut & SHA-256 Authenticode Signing:**
  - Embedded multi-resolution icon (`docs/assets/app-icon.ico`), `VS_VERSION_INFO` (v1.2.0.0), and `asInvoker` manifest via pure-Go COFF `.syso` generator (`Scripts/gen_winres.go`).
  - Switched Windows boot auto-start (`aronium-util service install`) from `HKCU\...\CurrentVersion\Run` to a standard Windows Startup Folder `.lnk` shortcut (`Aronium Utility Watcher.lnk`) and removed binary symbol stripping (`-s -w`) on Windows builds to minimize false-positive antivirus alerts.
  - Added SHA-256 Authenticode code-signing (`osslsigncode`) and bundled `AroniumUtility-CodeSigning.cer` in releases.

---

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
