# Aronium Utility — Especificación Técnica y Funcional (Spec-Driven Development)

**Proyecto:** `aronium-utility` (`aronium-util` + `aronium-util-setup`)  
**Versión:** `1.0.0`  
**Licencia:** MIT (Open Source)  
**Lenguaje y Motor:** 100% Go (`modernc.org/sqlite` puro sin CGO — compilación cruzada directa a Windows x64 y Windows x86)  
**Target Principal:** Comercios usuarios de **Aronium Lite** en Windows 10/11 (32-bit y 64-bit) con base de datos local SQLite (`%LOCALAPPDATA%\Aronium\Data\pos.db`)  

> **Nota de cumplimiento e independencia:** *Aronium Utility* es una herramienta CLI independiente y de código abierto diseñada para interoperabilidad, respaldo en caliente, sincronización con Google Drive / Google Sheets, gestión de cuentas de "Fiado" por cliente, operación multi-caja offline-first y extensión fiscal (DIAN Colombia) leyendo `pos.db` en modo estricto de solo lectura (`mode=ro`). No modifica binarios propietarios ni evade sistemas de licenciamiento.

---

## 1. Regla de Oro Arquitectónica sobre SQLite (`pos.db`)

Aronium Lite mantiene `pos.db` abierto durante la operación de caja. Para garantizar **cero bloqueos (`SQLITE_BUSY`)** y **cero corrupción**:

1. **Lectura Síncrona Estricta (`mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)`):**
   - Todas las consultas sobre `pos.db` se ejecutan en modo solo lectura con *exponential backoff* (`db.WithRetry`).
2. **Base de Estado Propia (`aronium_utility_state.db` en modo WAL):**
   - Almacena `sync_checkpoints`, `fiado_notes`, `customer_credit_limits`, `sync_outbox` (cola offline-first idempotente), `register_metadata`, `kardex_history`, `product_expirations`, `price_lists`, `gdrive_files`, `fiscal_config` y `fiscal_documents`.
3. **Consultas Cruzadas vía `ATTACH DATABASE` (`TODO-011`):**
   - Permite adjuntar `pos.db` como `aronium_pos` en solo lectura sobre `aronium_utility_state.db` sin alterar jamás el esquema original de Aronium.
4. **Respaldos en Caliente con `VACUUM INTO` (`TODO-038`):**
   - Genera snapshots transaccionalmente consistentes en caliente sin copiar archivos crudos mientras Aronium escribe.

---

## 2. Flujo Principal: Tabla Unificada de Inventario + Enrutamiento Automático de "Fiados" por Cliente hacia Google Drive

1. **Autenticación Directa vía Web (`aronium-util drive login` — `TODO-025`):**
   - Levanta un portal OAuth 2.0 local en `http://127.0.0.1:8765`, abre el navegador predeterminado de Windows, solicita autorización a la cuenta de Google del comerciante, intercambia el código OAuth por `access_token` + `refresh_token` y guarda las credenciales cifradas con **AES-256-GCM** en `gdrive.token.enc`.
2. **Detección Inteligente de "Fiados" (`TODO-017`..`TODO-019`):**
   - Detecta ventas no pagadas (`Document.PaidStatus = 0`), medios de pago configurados como crédito (`"Fiado"`, `"Crédito"`, `"Cuenta Corriente"`, `"Cartera"` o `PaymentType.MarkAsPaid = 0`) y **pagos divididos** (calculando `Saldo Pendiente = Document.Total - Suma(Pagos Efectivos)`).
   - Detecta abonos posteriores (`Payment`) y recalcula automáticamente la deuda histórica, el total abonado y el saldo vigente del cliente.
3. **Enrutamiento Automático por Nombre de Cliente (`TODO-018`):**
   - Al pasar una factura en Aronium asignada a un cliente (`Customer.Name` / `Customer.Id`) o etiquetada en la nota (`Cliente: <Nombre>`), la venta, el detalle de productos comprados y las anotaciones (`Document.Note`, `Document.InternalNote` y `fiado_notes` del CLI) se enrutan automáticamente a la cuenta individual de ese cliente.
4. **Almacenamiento en Google Drive y Google Sheets (`TODO-026`..`TODO-028`):**
   - Genera y sincroniza la tabla maestra multi-hoja **`Aronium_Inventario_y_Fiados.xlsx`** con pestañas:
     - `Resumen_Fiados` (consolidado de deuda, abonos, saldo, cupo, mora y estado por cliente).
     - `Detalle_Fiados` (historial de facturas fiadas, productos detallados, pagos divididos y notas).
     - `Inventario` (SKU, código de barras, producto, categoría, stock actual, stock mínimo, costo, precio de venta, valorización total y alertas `OK | STOCK_BAJO | AGOTADO | STOCK_NEGATIVO`).
     - `Cliente_<Nombre>` (pestaña individual por cada cliente con saldo o historial).
   - Además, almacena archivos individuales por cliente en `/AroniumUtility/Fiados/Clientes/<Nombre_Cliente>.xlsx`, `.csv` y `_Estado_Cuenta.txt`, manteniendo `file_id` estables en **Google Drive API v3** y actualizando **Google Sheets API v4** con cola offline (`sync_outbox`).

---

## 3. Cobertura Completa del Backlog (`TODO-001` a `TODO-069`) y Especificación Funcional (`PRO-FR-001` a `PRO-FR-007`)

| Fase | Rango de TODOs | Paquetes Go Implementados | Estado |
|---|---|---|:---:|
| **Fase 0: Gobernanza, Config & Doctor** | `TODO-001` .. `TODO-008` | `internal/config`, `internal/db` (`RunDoctor`), `.github/workflows/ci.yml` | Completado |
| **Fase 1: Motor Core RO, Estado & Watcher** | `TODO-009` .. `TODO-016` | `internal/db`, `internal/events`, `internal/service` | Completado |
| **Fase 2: Fiados, Inventario & Google Drive Web OAuth** | `TODO-017` .. `TODO-028` | `internal/fiados`, `internal/inventory`, `internal/sync` | Completado |
| **Fase 3: Multi-Estación, Cajas, Hub & Admin** | `TODO-029` .. `TODO-037` | `internal/registers`, `internal/hub`, `internal/admin` | Completado |
| **Fase 4: Backups en Caliente & Migración Multi-Motor** | `TODO-038` .. `TODO-045` | `internal/backup`, `internal/migrate` | Completado |
| **Fase 5: Hooks, Reportes, Moneda Dual & ESC/POS** | `TODO-046` .. `TODO-051` | `internal/events`, `internal/reports`, `internal/inventory` | Completado |
| **Fase 6: Conector Fiscal Modular DIAN (Colombia)** | `TODO-052` .. `TODO-056` | `internal/dian` (UBL 2.1, CUFE SHA-384, QR, Notas Crédito, Contingencia) | Completado |
| **Fase 7: QA (BDD / TC-001..030), Seguridad & Instalador** | `TODO-057` .. `TODO-069` | `internal/testutil`, `tests/bdd_integration_test.go`, `cmd/aronium-util-setup`, `installer/` | Completado |

---

## 4. Trazabilidad de Matriz de Pruebas QA (`TC-001` a `TC-030`)

- **`TC-001` a `TC-010` (Instalación, Conectividad, Cajas y Trazabilidad de Ventas):** Cubiertos por `cmd/aronium-util-setup`, `registers.ClaimIdentity`, `registers.QuerySales` y `hub.Server`.
- **`TC-011` a `TC-016` y `TC-021` a `TC-024` (Concurrencia, Venta Simultánea de Última Unidad, Edición Concurrente y Caída de Red LAN/Internet):** Verificados automáticamente en `TestBDD_MultiStationHubConflictsAndOfflineResilience` y `TestBDD_ConcurrentWritesWatcherAndVerifiedHotBackupRestore`.
- **`TC-017` y `TC-025` (Ciclo Completo de Backup en Caliente, Verificación SHA-256 / `PRAGMA integrity_check` y Restauración Segura):** Verificados en `TestBDD_ConcurrentWritesWatcherAndVerifiedHotBackupRestore`.
- **`TC-018` y `MIG-001`..`MIG-006` (Migración Lite -> SQL Server Express / PostgreSQL y Verificación Financiera):** Verificados en `TestBDD_MigrationDIANReportsAdminAndSecurity`.
- **`TC-026` (Rendimiento con Catálogo Masivo en Hardware de Gama Baja):** Verificado en `TestBDD_HighVolumePerformanceBenchmark_TC026`.
- **`TC-029` (Conflicto de Identidad de Caja Duplicada en dos Estaciones):** Verificado en `registers.ClaimIdentity` y `db.RunDoctor`.
- **`TC-030` (Advertencia de Caché en Memoria de Productos en Aronium):** Incorporado explícitamente en `hub.CatalogCacheWarning()`.
