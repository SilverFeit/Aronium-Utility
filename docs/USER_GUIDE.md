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
   - Instala la aplicación gráfica **`aronium-utility-ui.exe` (Aronium Utility UI)** en `%LOCALAPPDATA%\Programs\AroniumUtility\aronium-utility-ui.exe` y crea su acceso directo en el Escritorio con su ícono nativo.
   - Agrega la carpeta al **`PATH`** del usuario en Windows y configura las variables de entorno (`ARONIUM_UTIL_HOME`, `ARONIUM_UTIL_CONFIG`, `ARONIUM_POS_DB`, `ARONIUM_STATE_DB`, `ARONIUM_ACCOUNTS_DB`, `ARONIUM_STORE_ID`, `ARONIUM_REGISTER_ID`, `ARONIUM_CLI_BIN`, `ARONIUM_UI_BIN`).
   - Detecta instalaciones previas automáticamente, conserva `aronium-util.yaml` y rutas personalizadas de base de datos, y ejecuta la migración automática sin pérdida de datos en **`%LOCALAPPDATA%\AroniumUtility\cuentas.db`** (creando respaldo `.v1.bak`).
   - Si es instalación nueva, detecta `%LOCALAPPDATA%\Aronium\Data\pos.db`, genera `aronium-util.yaml` e inicializa `cuentas.db` (modo SQLite WAL).
   - Registra el arranque automático en la carpeta de inicio de Windows (`%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility.lnk`) para que **Aronium Utility se abra automáticamente al encender el PC** (con su ícono en la bandeja de tareas y su monitor POS en segundo plano). Esta opción se puede activar/desactivar como **check marcable** desde el menú de clic derecho en la bandeja de tareas.
4. *(Opcional — Certificado de Firma Digital SHA-256)* Todos los `.exe` están firmados con Authenticode (`AroniumUtility-CodeSigning.cer` incluido en la descarga). Si tu equipo Windows o antivirus corporativo muestra una advertencia de editor local, puedes instalar `AroniumUtility-CodeSigning.cer` con doble clic en **Equipo Local → Editores de confianza** y **Entidades de certificación raíz de confianza** *(o con PowerShell)*:
   ```powershell
   Import-Certificate -FilePath .\AroniumUtility-CodeSigning.cer -CertStoreLocation Cert:\CurrentUser\Root
   Import-Certificate -FilePath .\AroniumUtility-CodeSigning.cer -CertStoreLocation Cert:\CurrentUser\TrustedPublisher
   ```

---

## Paso 2: Cómo Abrir Aronium Utility UI (Interfaz Gráfica Tauri 2 + Svelte)

El dependiente o administrador puede abrir la interfaz visual de cualquiera de estas dos formas:

1. **Desde el Ejecutable Directo / Acceso Directo en el Escritorio:**
   Haciendo doble clic en **`Aronium Utility UI`** (`aronium-utility-ui.exe`).
2. **Desde el CLI en cualquier terminal (CMD o PowerShell):**
   ```powershell
   aronium-util ui
   ```

Desde **Aronium Utility UI** (con el interruptor superior entre **`Modo Básico`** para cajeros/dependientes y **`Modo Avanzado`** para diagnóstico/administración) podrás:
- **Detectar o seleccionar `pos.db` visualmente:** Si Aronium Lite está instalado en otra ruta o unidad, la UI muestra un aviso amigable y permite seleccionar el archivo `.db` sin usar la terminal.
- **Crear Cuentas Compartidas (`Texto-Número`) y Anotar Artículos o Valores Puntuales (`cuentas.db`):** Captura el **ID compuesto en 2 partes** (ej. `ABC` + `-` + `123` = `ABC-123` o `Torre-110`), **`Nombre`** (separa varias cuentas con el mismo ID), **`Método de Pago`** (incluyendo **`QR`**), **`Tope de Crédito`**, **`Teléfono` (opcional)** y **`Descripción` (opcional)**. Arriba del escáner HID incluye **Agregar Valor Puntual** (con descripción opcional, botones **`-100` / `+100`** y accesos rápidos **`+$500`**, **`+$1.000`**, **`+$2.000`**, **`+$5.000`**).
- **Registrar Abonos sin Ambigüedad y Verificar Inventario Neto:** Al registrar pagos en una cuenta con ID compartido, muestra todas las cuentas que comparten ese ID para seleccionar la persona correcta, calcula `Stock_Neto = Stock_Aronium - Unidades_En_Cuentas` y no altera el cierre de caja de Aronium.
- **Ver Dashboard y Métricas en Vivo** (ventas totales, ventas por caja registradora, cartera pendiente e inventario).
- **Conectar y Sincronizar Google Drive / Sheets con 1 Clic** y crear respaldos en caliente (`VACUUM INTO`).

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
- **`Aronium_Inventario_y_Cuentas_Credito.xlsx`** (con 4 pestañas consolidadas: **`Cuentas`**, **`Movimientos`**, **`Detalle_Articulos`**, **`Inventario_Neto`** y pestañas individuales por cliente).
- **`/AroniumUtility/Cuentas_Credito/Clientes/<Nombre_Cliente>.xlsx`**, `.csv` y `_Estado_Cuenta.txt`.
- Respaldo en caliente de `cuentas.db` (`cuentas_backup.db` mediante SQLite `VACUUM INTO`).
