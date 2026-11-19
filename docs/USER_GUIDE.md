# Guía de Usuario Paso a Paso — Aronium Utility & Aronium Utility UI (Windows + Google Drive)

Esta guía explica cómo instalar y utilizar **Aronium Utility** (el motor CLI en Go que corre en segundo plano desde que se enciende el equipo) y **Aronium Utility UI** (la interfaz gráfica de escritorio en **Tauri 2 + Svelte + TypeScript**) junto con **Aronium Lite** en Windows.

---

## Paso 1: Instalación Unificada en Windows (Crea el Ejecutable del CLI y el Ejecutable de la UI)

1. Descarga **`aronium-util-setup-x64.exe`** (o `aronium-util-setup-x86.exe` para equipos Windows de 32 bits) desde [**GitHub Releases**](https://github.com/SilverFeit/Aronium-Utility/releases/latest).
2. Haz doble clic sobre **`aronium-util-setup-x64.exe`** (o ejecútalo desde terminal):
   ```powershell
   .\aronium-util-setup-x64.exe --register POS-01 --store-id STORE-01 --launch-ui
   ```
3. El instalador realiza automáticamente cinco tareas en una sola instalación:
   - Instala el motor CLI **`aronium-util.exe`** en `%LOCALAPPDATA%\Programs\AroniumUtility\aronium-util.exe`.
   - Instala la aplicación gráfica **`aronium-utility-ui.exe` (Aronium Utility UI)** en `%LOCALAPPDATA%\Programs\AroniumUtility\aronium-utility-ui.exe` y crea su acceso directo en el Escritorio.
   - Agrega la carpeta al **`PATH`** del usuario en Windows y configura las variables de entorno (`ARONIUM_UTIL_HOME`, `ARONIUM_UTIL_CONFIG`, `ARONIUM_POS_DB`, `ARONIUM_STATE_DB`, `ARONIUM_STORE_ID`, `ARONIUM_REGISTER_ID`, `ARONIUM_CLI_BIN`, `ARONIUM_UI_BIN`).
   - Detecta automáticamente `%LOCALAPPDATA%\Aronium\Data\pos.db` y genera `%LOCALAPPDATA%\AroniumUtility\aronium-util.yaml`.
   - Registra `aronium-util.exe watch` en el arranque de Windows para que **el CLI se ejecute automáticamente apenas se encienda el equipo** y procese las transacciones en vivo sobre `pos.db`.

---

## Paso 2: Cómo Abrir Aronium Utility UI (Interfaz Gráfica Tauri 2 + Svelte)

El dependiente o administrador puede abrir la interfaz visual de cualquiera de estas dos formas:

1. **Desde el Ejecutable Directo / Acceso Directo en el Escritorio:**
   Haciendo doble clic en **`Aronium Utility UI`** (`aronium-utility-ui.exe`).
2. **Desde el CLI en cualquier terminal (CMD o PowerShell):**
   ```powershell
   aronium-util ui
   ```

Desde **Aronium Utility UI** podrás:
- Ver el **Dashboard y Métricas en Vivo** (ventas totales, ventas por caja registradora, top productos vendidos, valorización de inventario y cartera de crédito pendiente).
- Gestionar **Cuentas de Crédito por Cliente** (buscar clientes, ver facturas y productos comprados, agregar anotaciones o compromisos de pago, configurar límites de crédito y generar estados de cuenta).
- Validar **Inventario Vivo, Alertas de Stock Bajo/Agotado y Kardex de Movimientos**.
- Conectar y sincronizar **Google Drive / Google Sheets** con 1 clic y crear **Respaldos en Caliente (`VACUUM INTO` + AES-256-GCM)**.

---

## Paso 3: Configurar el Medio de Pago a Crédito en Aronium Lite

1. Abre **Aronium Lite** y entra a **`Management` (Administración) → `Payment types` (Tipos de pago)**.
2. Haz clic en **`Add payment type` (Agregar tipo de pago)** con estos valores:
   - **Name (Nombre):** `Crédito` *(también se reconocen automáticamente `Cuenta Corriente`, `Cuenta Cliente`, `Credit` o `Cartera`)*.
   - **Customer required (Cliente obligatorio):** **Activado (`ON`)**.
   - **Mark transaction as paid (Marcar transacción como pagada):** **Desactivado (`OFF`)**.
3. Guarda los cambios. Cada vez que pases una venta asignada al nombre de un cliente con ese medio de pago (o pago dividido), el motor la enrutará automáticamente a la cuenta individual del cliente y la sincronizará en tu Google Drive.

---

## Paso 4: Conectar Google Drive (Autenticación vía Web)

Puedes conectar tu cuenta de Google Drive haciendo clic en **"Autenticar Google Drive vía Web (OAuth 2.0)"** dentro de la pestaña **5. Google Drive & Backups** de **Aronium Utility UI**, o ejecutando desde terminal:

```powershell
aronium-util drive login
```

Esto actualiza automáticamente en tu Google Drive:
- **`Aronium_Inventario_y_Cuentas_Credito.xlsx`** (con pestañas `Resumen_Cuentas_Credito`, `Detalle_Cuentas_Credito`, `Inventario` y una pestaña `Cliente_<Nombre>` por cada cliente).
- **`/AroniumUtility/Cuentas_Credito/Clientes/<Nombre_Cliente>.xlsx`**, `.csv` y `_Estado_Cuenta.txt`.
