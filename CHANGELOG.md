# Changelog

All notable changes to **Aronium Utility** are documented in this file.

## [1.2.7] — 2026-11-22

### Improved
- **Simplified 1-Click Google Drive & Google Sheets Web OAuth Portal (`internal/sync/gdrive.go`, CLI, UI):**
  - Redesigned `webAuthPortalHTML` with direct Google Cloud Console links (`drive.googleapis.com`, `sheets.googleapis.com`, `credentials`), an interactive **`Copiar URI`** button next to `http://127.0.0.1:8765/oauth2/callback`, and a collapsible **`¿Solo quieres probar rápido? (Avanzado)`** `<details>` section housing Opción B and Google OAuth Playground (`developers.google.com/oauthplayground/`).
  - Added automatic extraction of `client_id` and `client_secret` from Google Cloud's **`credentials.json`** (`ParseGoogleCredentialsJSON` + `AutoDetectGoogleCredentialsJSON` + drag/upload picker in the web portal and `--credentials-json` CLI flag).
  - Enabled **1-Click Google Sign-In (`?direct_redirect=1`)** from the non-technical **Modo Básico** UI (`Conectar con Google (1 Clic)`) and `--one-click` CLI flag whenever OAuth credentials (`ARONIUM_GDRIVE_CLIENT_ID` / `credentials.json` / `aronium-util.yaml`) are configured.

---

## [1.2.6] — 2026-11-22

### Added
- **Windows System Tray Controller (`cmd/aronium-utility-ui/tray_windows.go`, `internal/service`):**
  - Pure-Go (`CGO_ENABLED=0`) native Win32 System Tray icon in the taskbar notification area (`Shell_NotifyIconW`) with multi-resolution icon, Explorer restart recovery (`TaskbarCreated`), and single-instance window reopen lock (`ui-instance.json`).
  - Right-click context menu inside the taskbar tray:
    - **`Abrir Aronium Utility`**: Opens or brings back the desktop app window.
    - **`✓ Iniciar al encender el sistema (Abrir automáticamente)`**: Native checkable menu item (`MF_CHECKED` / `MF_UNCHECKED`) toggled in 1 click, synced with `%LOCALAPPDATA%\AroniumUtility\autostart.pref` and the `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility.lnk` shortcut so Aronium Utility opens automatically whenever the PC boots.
    - **`Buscar y actualizar desde GitHub`**: Checks GitHub Releases (also pre-checks in background to show available version tags), downloads the SHA-256 verified installer, and applies updates seamlessly.
    - **`Cerrar del todo`**: Cleanly stops the background watcher (`aronium-util watch`), closes the UI window process, removes the tray icon, and exits completely.
- **Hidden Child Console Execution & Synchronized UI Autostart Checkbox:**
  - Configured `CREATE_NO_WINDOW` / `HideWindow` for background `aronium-util.exe` and updater child invocations on Windows.
  - Added CLI `aronium-util service autostart [status|enable|disable|toggle] --json` and a matching checkbox inside **Modo Avanzado → `6. Diagnóstico & Actualización`**.

---

## [1.2.5] — 2026-11-22

### Changed
- **Generic Form Placeholders & Cleaner Title (`ui/src/App.svelte`, CLI, Docs):**
  - Replaced form sample placeholders with generic values (`ABC-123` for compound ID, `000-000-0000` for optional phone, `Torre A, Apartamento 110` for optional description, `Nombre Apellido` for name).
  - Simplified the quick-value section header to **`Agregar Valor Puntual`** (removed redundant parenthetical examples).

---

## [1.2.4] — 2026-11-22

### Added
- **Delete Settled Accounts from Advanced View & CLI (`saldo_actual == 0` Guard):**
  - Added `DeletePaidAccount` in `internal/accounts` enforcing strictly zero balance (`saldo_actual == 0`), cascading ledger cleanup, and immediate update of the recovery snapshot (`cuentas_pre_install_snapshot.db`).
  - Added **`Acciones → Eliminar`** button with inline confirmation inside **Modo Avanzado → `2. Ledger Cuentas (cuentas.db)`** (enabled only when `saldo_actual === 0`; accounts with pending balance show `Saldo pendiente` and cannot be deleted).
  - Added CLI subcommand `aronium-util accounts delete --account-id <id> --name <nombre>`.

---

## [1.2.3] — 2026-11-22

### Added
- **Direct In-App GitHub Releases Auto-Updater (`Modo Avanzado` UI & `aronium-util update check|apply`):**
  - Added `internal/updater` package and CLI subcommands `aronium-util update check` and `aronium-util update apply` querying `https://api.github.com/repos/SilverFeit/Aronium-Utility/releases/latest`.
  - Added the **"Actualización Directa desde GitHub Releases"** panel inside **Modo Avanzado → `6. Diagnóstico & Actualización`** (plus a quick button in the top bar) to check for newer GitHub releases, view release notes, download the matching Windows unified installer (`aronium-util-setup-x64.exe` / `x86.exe`), verify its SHA-256 hash against `SHA256SUMS.txt`, close active processes, update in-place preserving `cuentas.db` and `aronium-util.yaml`, and relaunch **Aronium Utility UI** automatically.

---

## [1.2.2] — 2026-11-22

### Fixed
- **Automatic Process Termination Before Update/Uninstall (`cmd/aronium-util-setup`, `installer/*.bat`, `installer/install.ps1`):**
  - Resolves the Windows file-lock error (`open ...\aronium-utility-ui.exe: El proceso no tiene acceso al archivo porque está siendo utilizado por otro proceso`) when running the installer while **Aronium Utility UI** or `aronium-util.exe watch` is open.
  - The installer now automatically stops running `aronium-utility-ui.exe` and `aronium-util.exe` processes (`taskkill /F /T /IM` + PowerShell `Stop-Process`), uses an atomic `.old` rename fallback with retry (`writeExecutableSafely`), and automatically relaunches **Aronium Utility UI** if it was open prior to the update.
- **Database & Account Preservation Across Uninstall / Reinstall (`internal/accounts`, `cmd/aronium-util-setup`):**
  - Automatically maintains a hot SQLite snapshot (`backups/cuentas_pre_install_snapshot.db`) after every account creation, charge, or payment.
  - Uninstalling removes only the program binaries in `%LOCALAPPDATA%\Programs\AroniumUtility`, preserving `%LOCALAPPDATA%\AroniumUtility\cuentas.db`, `aronium-util.yaml`, and backups intact—and automatically restores `cuentas.db` from local or Google Drive backups if `cuentas.db` was deleted before reinstalling.

---

## [1.2.1] — 2026-11-22

### Fixed
- **Windows Executable & Shortcut Icon Brightness Fix (`Scripts/gen_winres.go`, `cmd/aronium-util-setup`, `internal/service`):**
  - Fixed a 4x supersampling alpha normalization bug in `renderAppIcon` where dividing accumulated RGB channels by `avgA` (0..255) instead of `afSum` dimmed icons to ~6% luminance (appearing almost pitch black in Windows Explorer and shortcuts).
  - Brightened vector gradient & contrast palette, bundled `app-icon.ico` directly in installation output and `.zip` packages, and added `ie4uinit.exe -show` / `-ClearIconCache` refresh on Windows desktop/startup shortcut creation.

### Added
- **Compound Two-Part ID (`Texto-Numero`, e.g., `ABC-123`) & Shared Apartment Accounts (`cuentas.db` Schema v2):**
  - Dual-part unified ID field (`Texto` + `-` + `Número`) with live hyphen preview badge (`ABC-123`).
  - Allows multiple independent customer accounts to share the same compound ID (differentiated by `nombre`) and displays all accounts sharing that ID during payment registration (`Registrar Pago / Abono`) so the clerk picks the exact person.
  - Made **Teléfono** and **Descripción / Referencia** optional, and added **`QR`** as a payment method option across account creation, payments, and CLI flags (`--payment-method QR`, `--id-prefix`, `--id-number`, `--name`, `accounts by-id`).
- **Quick Punctual Value Annotator (`Agregar Valor Puntual` above Scanner):**
  - Added quick custom value panel directly above *"Agregar Artículos con Escáner o Buscador (Opcional)"* with optional item description, manual amount input flanked by **`-100` / `+100`** step buttons, and 1-click preset value buttons (**`+$500`**, **`+$1.000`**, **`+$2.000`**, **`+$5.000`**).
- **Automatic Upgrade Detection & Lossless `cuentas.db` Schema Migration (v1 → v2):**
  - Both the unified installer (`aronium-util-setup`) and CLI (`init`, `accounts migrate`, `OpenAccountsDB`) detect existing installations, preserve `aronium-util.yaml` configuration and custom DB paths, save a pre-migration `.v1.bak` backup, and losslessly migrate v1 `cuentas.db` tables to v2 (`account_key`) preserving 100% of accounts, movements, and frozen item snapshots.

---

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
