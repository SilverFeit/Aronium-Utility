# Aronium Utility & Aronium Utility UI — Especificación Técnica y Funcional (Spec-Driven Development)

**Proyecto:** `aronium-utility` (`aronium-util.exe` + `aronium-utility-ui.exe` + `aronium-util-setup.exe`)  
**Versión:** `1.2.0`  
**Licencia:** MIT (Open Source)  
**Stack de Arquitectura:**
- **Motor Core & Sidecar CLI:** 100% Go (`modernc.org/sqlite` puro sin CGO — compilación cruzada directa a Windows x64 y Windows x86 con recursos PE `.syso` y firma Authenticode SHA-256)
- **Interfaz Gráfica de Escritorio (`Aronium Utility UI`):** **Tauri 2 + Svelte 5 + TypeScript** (`ui/` + `ui/src-tauri/` + WebView nativo del sistema operativo)

---

## 1. Arquitectura de Doble Base de Datos: `pos.db` (Solo Lectura) + `cuentas.db` (Libro Mayor WAL)

```text
Desktop UI — Svelte 5 + TypeScript (Aronium Utility UI: Modo Básico / Modo Avanzado)
       │
       ▼  Tauri 2 IPC / Commands (JSON)
Tauri Core — Puente Sidecar Nativo (`aronium-utility-ui.exe`)
       │
       ▼  Sidecar Subprocess (`aronium-util.exe <cmd> --json`)
CLI Existente en Go (`aronium-util.exe`)
       ├──► pos.db (Aronium Lite - STRICT mode=ro): Catálogo en vivo, códigos de barras y stock POS
       ├──► cuentas.db (SQLite WAL propia): Cuentas, Movimientos (CARGO/ABONO/AJUSTE) y Snapshots congelados
       ├──► aronium_utility_state.db (SQLite WAL): Checkpoints, outbox offline-first, metadata de cajas
       └──► Google Drive API v3 & Sheets API v4: Excel 4 hojas + Backup binario en caliente (`VACUUM INTO`)
```

### Reglas de Consistencia e Integridad
1. **Aislamiento de Cierre Diario en Aronium:** Los pagos y abonos de cuentas mensuales se registran exclusivamente en `cuentas.db` (`movimientos` tipo `ABONO`) para no contaminar el balance de ventas diarias de Aronium.
2. **Consumo Directo de Productos desde `pos.db`:** El buscador y el lector de códigos de barras HID consultan en vivo el catálogo real de Aronium (`Product`, `Barcode`, `ProductGroup`, `Stock`) indexado por `mtime`.
3. **Snapshots Congelados (`movimiento_items`):** Al anotar un artículo en `cuentas.db`, se guarda una copia inmutable del nombre y precio unitario vigente en ese instante, protegiendo las cuentas históricas ante futuros cambios de precio en Aronium.
4. **Inventario Neto (`Ruta A`):** `Stock_Neto = Stock_Aronium - Unidades_Pendientes_En_Cuentas`.
5. **Experiencia Cero-Técnica (`Modo Básico` por defecto):** Interruptor superior `Modo Básico` / `Modo Avanzado`, selector visual de bases de datos con autodetección y mensajes claros, escáner con foco continuo y firma digital Authenticode + ícono PE nativo para minimizar falsos positivos de antivirus.
