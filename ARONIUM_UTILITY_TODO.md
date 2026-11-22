# Aronium Utility — Plan de Arquitectura y To-Do Detallado (v1.2.0)

**Arquitectura:** Doble Base de Datos (`pos.db` + `cuentas.db`) · Motor Sidecar Go CLI (`aronium-util.exe`) · Interfaz Gráfica Tauri 2 + Svelte 5 + TypeScript (`aronium-utility-ui.exe`) · Instalador Nativo Firmado con Ícono

---

## 1. Arquitectura de Doble Base de Datos (Ruta A — Cero Contaminación de Caja)

### Problema Resuelto
Registrar pagos de cuentas de crédito mensuales (por ejemplo, 2,000,000) dentro del flujo de Aronium Lite distorsiona el cierre diario de caja porque suma recaudo de cartera de semanas anteriores a las ventas de contado del día.

### Decisión Arquitectónica: **Ruta A (`pos.db` Solo Lectura + `cuentas.db` Aislada + Inventario Neto)**
- **`pos.db` (`%LOCALAPPDATA%\Aronium\Data\pos.db`):**
  - Se abre estrictamente en **solo lectura** (`mode=ro&_busy_timeout=5000`).
  - Se utiliza directamente para consultar en vivo el catálogo de productos (`Product`, `ProductGroup`, `Barcode`, `Stock`) y las ventas generales del POS. Cualquier producto nuevo o actualización de precio en Aronium se refleja al instante mediante detección de `mtime`.
- **`cuentas.db` (`%LOCALAPPDATA%\AroniumUtility\cuentas.db`):**
  - Base de datos SQLite independiente en **modo WAL** (`journal_mode=WAL`), **creada automáticamente durante la instalación** e inicialización.
  - Libro mayor (ledger) inmutable con 3 tablas:
    - `cuentas`: `id` (PK, documento/identificador único), `nombre`, `telefono`, `metodo_pago`, `tope_credito` (entero), `descripcion`, `estado` (`ACTIVA` | `BLOQUEADA` | `CERRADA`), `creado_en`, `actualizado_en`.
    - `movimientos`: `id` (PK), `cuenta_id` (FK), `tipo` (`CARGO` | `ABONO` | `AJUSTE`), `monto` (entero), `metodo_pago_usado`, `nota`, `fecha`. Saldo = `SUM(CARGO) - SUM(ABONO) + SUM(AJUSTE)`.
    - `movimiento_items`: `id` (PK), `movimiento_id` (FK), `aronium_product_id`, `codigo_barras`, `nombre_snapshot`, `precio_unitario_snapshot` (entero congelado al momento del cargo), `cantidad`, `subtotal` (entero).
- **Cálculo de Inventario Neto (`Stock_Neto`):**
  - `Stock_Neto = Stock_Aronium (pos.db) - Unidades_Pendientes_En_Cuentas (cuentas.db)`.
- **Snapshots Congelados:**
  - Si un producto sube o baja de precio en Aronium posteriormente, los artículos anotados en `movimiento_items` conservan el nombre y precio unitario congelados al momento de anotar la cuenta.

---

## 2. Backlog Detallado (To-Do v1.2.0)

### Fase A — Base de Datos Dedicada `cuentas.db` y Motor de Inventario Neto
- [x] **TODO-101:** Añadir `accounts_db_path` (`cuentas.db`) y `credit_limit_policy` (`warn` / `strict`) en `internal/config/config.go` con variable de entorno `ARONIUM_ACCOUNTS_DB`.
- [x] **TODO-102:** Implementar `internal/accounts/accounts.go` con SQLite WAL (`cuentas`, `movimientos`, `movimiento_items`), transacciones atómicas `BEGIN IMMEDIATE`, cálculo de libro mayor, validación de tope de crédito, cómputo de unidades comprometidas por producto y snapshot en caliente (`VACUUM INTO`).
- [x] **TODO-103:** Actualizar `internal/db/db.go` con soporte multi-código de barras (`Barcode` + `Product.Code`), búsqueda optimizada para escáner HID (`SearchCatalogForScanner`) y caché indexado por `mtime` de `pos.db`.
- [x] **TODO-104:** Enriquecer `internal/inventory/inventory.go` con `aronium_stock`, `committed_in_accounts_qty` y `net_stock_quantity` (`Stock_Aronium - Unidades_En_Cuentas`).

### Fase B — Comandos CLI y Sincronización Asíncrona con Google Drive
- [x] **TODO-105:** Exponer en `cmd/aronium-util/main.go` los subcomandos `--json`:
  - `accounts list|create|charge|pay|statement`
  - `catalog search [--query] [--barcode]`
  - `config set-db [--pos-db] [--accounts-db]`
- [x] **TODO-106:** Actualizar `internal/sync/xlsx.go` y `internal/sync/gdrive.go` para generar el libro maestro `Aronium_Inventario_y_Cuentas_Credito.xlsx` con las 4 hojas requeridas (`Cuentas`, `Movimientos`, `Detalle_Articulos`, `Inventario_Neto`), carpetas por cliente y respaldo binario en caliente (`VACUUM INTO`) de `cuentas.db` (`cuentas_backup.db`).

### Fase C — Interfaz Gráfica `Aronium Utility UI` (Modo Básico / Modo Avanzado)
- [x] **TODO-107:** Agregar en la barra superior de `ui/src/App.svelte` un interruptor prominente **`Modo Básico` / `Modo Avanzado`** (iniciando en **Modo Básico** para usuarios sin conocimientos técnicos).
- [x] **TODO-108:** Añadir banner superior de estado y selector de bases de datos (`pos.db` de Aronium y `cuentas.db`): intenta usar la ruta por defecto `%LOCALAPPDATA%\Aronium\Data\pos.db`; si no la encuentra, informa en lenguaje sencillo qué hacer y permite seleccionar la ruta o crear una base de demostración con 1 clic.
- [x] **TODO-109:** Construir en **Modo Básico** el flujo dedicado **"Crear Cuenta / Anotar Artículos"** con:
  - Campos obligatorios: ID, Nombre, Teléfono, Método de Pago, Tope de Crédito y Descripción.
  - Sección opcional de artículos anotados consultados en vivo desde `pos.db`: campo único optimizado para lector de códigos de barras HID (`Enter` suma `+1` y mantiene el foco automáticamente sin usar el mouse) y búsqueda por texto.
  - Barra en vivo de validación de tope (`Saldo Actual + Nuevo Cargo / Tope`).
  - Guardado atómico en una sola transacción en `cuentas.db` + sincronización automática en segundo plano con Google Drive.
- [x] **TODO-110:** Construir en **Modo Básico** la vista **"Ver Cuentas y Registrar Pagos"** para consultar saldos, anotar más artículos, ver historial/recibo y registrar abonos (`ABONO` en `cuentas.db`) sin contaminar la caja diaria de Aronium.
- [x] **TODO-111:** Conservar en **Modo Avanzado** todas las vistas técnicas (Dashboard consolidado, Kardex, Cajas Registradoras, Backups AES-256-GCM, Migración SQL, Facturación UBL 2.1, Doctor y Servicio de Windows).

### Fase D — Instalador Unificado, Ícono Nativo y Firma Digital Anti-Antivirus (Avast)
- [x] **TODO-112:** Generar ícono multi-resolución (`docs/assets/app-icon.ico`: 256, 64, 48, 32, 16 px) y compilar recursos Windows PE COFF (`.syso` para `amd64` y `386`) con `RT_GROUP_ICON`, `RT_MANIFEST` (`asInvoker`, Windows 10/11) y `RT_VERSION` (`VS_VERSION_INFO` con compañía, descripción y copyright).
- [x] **TODO-113:** Conservar tablas de símbolos estándar en builds de Windows (eliminar `-s -w`) y registrar el inicio automático mediante acceso directo estándar en la carpeta `Startup` de Windows (evitando modificaciones directas del registro `CurrentVersion\Run` que activan heurísticas de Avast).
- [x] **TODO-114:** Firmar digitalmente (Authenticode SHA-256) los ejecutables Windows (`aronium-util.exe`, `aronium-utility-ui.exe`, `aronium-util-setup-x64.exe`, `aronium-util-setup-x86.exe`) mediante `openssl` + `osslsigncode` e incluir el certificado público `AroniumUtility-CodeSigning.cer` en el paquete de lanzamiento.
- [x] **TODO-115:** Crear `cuentas.db` automáticamente durante la instalación en `cmd/aronium-util-setup/main.go` y `installer/install.bat` / `install.ps1`.

### Fase E — Verificación Integral y Publicación (`v1.2.0`)
- [x] **TODO-116:** Pruebas automatizadas BDD/QA cubriendo escaneo continuo de 10 productos, congelación de snapshot tras cambio de precio en `pos.db`, validación de tope de crédito, concurrencia WAL (`pos.db` + `cuentas.db`) sin `SQLITE_BUSY`, cálculo de inventario neto y sincronización Drive (`.xlsx` + backup `VACUUM INTO`).
- [x] **TODO-117:** Empaquetado de release `v1.2.0`, push a GitHub, publicación de Release y verificación en verde de GitHub Actions CI.
