# Aronium Utility & Aronium Utility UI — Especificación Técnica y Funcional (Spec-Driven Development)

**Proyecto:** `aronium-utility` (`aronium-util.exe` + `aronium-utility-ui.exe` + `aronium-util-setup.exe`)  
**Versión:** `1.1.0`  
**Licencia:** MIT (Open Source)  
**Stack de Arquitectura:**
- **Motor Core & Sidecar CLI:** 100% Go (`modernc.org/sqlite` puro sin CGO — compilación cruzada directa a Windows x64 y Windows x86)
- **Interfaz Gráfica de Escritorio (`Aronium Utility UI`):** **Tauri 2 + Svelte 5 + TypeScript** (`ui/` + `ui/src-tauri/` + WebView nativo del sistema operativo sin empaquetar Chromium/Electron)

---

## 1. Arquitectura Desacoplada: Tauri 2 + Svelte + TypeScript → Sidecar Go CLI (`aronium-util`)

```text
Desktop UI — Svelte 5 + TypeScript (Aronium Utility UI)
Componentes, formularios de cuentas de crédito, tablas de inventario, dashboard y métricas
       │
       ▼  Tauri 2 IPC / Commands (JSON)
Tauri Core — Rust / Puente Sidecar Nativo (`aronium-utility-ui.exe`)
Gestión de procesos, permisos y comunicación IPC con el binario auxiliar
       │
       ▼  Sidecar Subprocess (`aronium-util.exe <cmd> --json`)
CLI Existente en Go (`aronium-util.exe`)
100% de la lógica de negocio, watcher en vivo desde el encendido del PC y sincronización
       │
       ▼  SQLite Read-Only (`mode=ro`) + Bóveda AES-256-GCM + OAuth 2.0
Datos y Resultados (`pos.db`, `aronium_utility_state.db`, Google Drive API v3 & Sheets API v4)
```

### Principios de Diseño
1. **Cero Duplicación de Lógica de Negocio:** La interfaz gráfica (`Aronium Utility UI`) no accede directamente a `pos.db` ni reescribe reglas contables; invoca al CLI (`aronium-util.exe`) mediante IPC (`admin --json`, `credit list --json`, `credit note add --json`, `credit limit --json`, `inventory list --json`, `inventory kardex --json`, `sync --json`, `doctor --json`, `backup create --json`) y presenta sus resultados JSON.
2. **Operación Automática desde el Encendido del Equipo:** Al instalar la suite, `aronium-util.exe watch` queda registrado en el inicio automático de Windows (`HKCU\Software\Microsoft\Windows\CurrentVersion\Run` y tareas programadas) para operar en segundo plano sobre todas las transacciones en vivo apenas se enciende el computador de caja.
3. **Doble Vía de Lanzamiento para el Dependiente:** Cuando el dependiente o administrador desea consultar el dashboard, validar inventario o agregar una anotación/cuenta de crédito de manera visual, puede abrir **Aronium Utility UI** haciendo doble clic en **`aronium-utility-ui.exe`** (o desde el acceso directo en el Escritorio) o ejecutando **`aronium-util ui`** desde la terminal.
4. **Instalación Unificada Sencilla:** Un único instalador (`aronium-util-setup-x64.exe` / `aronium-util-setup-x86.exe`) instala tanto el ejecutable del CLI (`aronium-util.exe`) como el ejecutable de la interfaz gráfica (`aronium-utility-ui.exe`), configura el `PATH` y las variables de entorno de Windows, y registra el servicio de inicio automático.
