# Guía de Usuario Paso a Paso — Aronium Utility (Windows + Google Drive + Fiados)

Esta guía está diseñada para dueños de tiendas, minimercados, ferreterías, papelerías y comercios que utilizan **Aronium Lite** en Windows y desean llevar su tabla de **Inventario Vivo** y las cuentas de **"Fiado" por Cliente** sincronizadas automáticamente en **Google Drive / Google Sheets**.

---

## Paso 1: Instalación en Windows (1 Clic — Configura CLI y Variables de Entorno)

No necesitas configurar rutas manualmente. El paquete de lanzamiento incluye un **Instalador Nativo Sencillo**:

### Opción A — Ejecutable Instalador (`aronium-util-setup-x64.exe`)
1. Descarga **`aronium-util-setup-x64.exe`** (o `aronium-util-setup-x86.exe` para equipos Windows de 32 bits) desde [**GitHub Releases**](https://github.com/SilverFeit/Aronium-Utility/releases/latest).
2. Haz doble clic sobre **`aronium-util-setup-x64.exe`** (o ejecútalo desde terminal):
   ```powershell
   .\aronium-util-setup-x64.exe --register POS-01 --store-id TIENDA-01
   ```
3. El instalador realiza automáticamente cuatro tareas:
   - Instala `aronium-util.exe` en `%LOCALAPPDATA%\Programs\AroniumUtility\aronium-util.exe`.
   - Agrega esa carpeta al **`PATH`** del usuario en Windows para que puedas escribir `aronium-util` desde cualquier ventana de CMD o PowerShell.
   - Configura las variables de entorno en Windows (`ARONIUM_UTIL_HOME`, `ARONIUM_UTIL_CONFIG`, `ARONIUM_POS_DB`, `ARONIUM_STATE_DB`, `ARONIUM_STORE_ID`, `ARONIUM_REGISTER_ID`).
   - Detecta automáticamente `%LOCALAPPDATA%\Aronium\Data\pos.db` y crea el archivo de configuración `%LOCALAPPDATA%\AroniumUtility\aronium-util.yaml`.

### Opción B — Script `install.bat` o `install.ps1`
Si descargaste el archivo `.zip`, descomprímelo y haz doble clic en **`install.bat`** (o ejecuta `.\install.ps1` en PowerShell). Para desinstalar y limpiar variables de entorno en cualquier momento, ejecuta `aronium-util-setup-x64.exe --uninstall` o `uninstall.bat`.

---

## Paso 2: Configurar el Medio de Pago "Fiado" en Aronium Lite

Para que cuando pases una cuenta con el nombre de un cliente vaya directamente a su cuenta de cobro en Google Drive:

1. Abre **Aronium Lite** y entra a **`Management` (Administración) → `Payment types` (Tipos de pago)**.
2. Haz clic en **`Add payment type` (Agregar tipo de pago)** con estos valores:
   - **Name (Nombre):** `Fiado` *(también se reconocen automáticamente `Crédito`, `Cuenta Corriente` o `Cartera`)*.
   - **Customer required (Cliente obligatorio):** **Activado (`ON`)** — así Aronium siempre te pedirá seleccionar el nombre del cliente al pasar una cuenta fiada.
   - **Mark transaction as paid (Marcar transacción como pagada):** **Desactivado (`OFF`)** — así la factura queda con estado pendiente hasta que el cliente abone o pague.
3. Guarda los cambios.

> **¿Cómo funciona en el mostrador?**
> - Cuando un cliente compra a crédito, seleccionas su nombre en Aronium (ej. *"Don Pedro Pérez"*), opcionalmente escribes una nota en la venta (ej. *"Lleva mercado quincena"*) y cierras la venta con el botón **`Fiado`** (o pago dividido: parte en `Efectivo` y el resto en `Fiado`).
> - **Aronium Utility** detecta la venta en segundos, identifica el nombre del cliente, toma los productos comprados, las notas y el saldo pendiente, y los agrega automáticamente a la cuenta de *"Don Pedro Pérez"* en tu Google Drive.

---

## Paso 3: Conectar Google Drive desde el CLI (Autenticación vía Web)

Abre una ventana de **PowerShell** o **CMD** y ejecuta:

```powershell
aronium-util drive login
```

1. El CLI abrirá automáticamente tu navegador web en el portal local de autenticación (`http://127.0.0.1:8765`).
2. Inicia sesión con tu cuenta de Google / Gmail y autoriza el acceso a Google Drive y Google Sheets (o vincula tu token OAuth / Client ID directamente en la página web que se abre).
3. Verás en el navegador el mensaje: **"¡Google Drive Conectado con Éxito!"**. El token queda guardado y cifrado con **AES-256-GCM** en `%LOCALAPPDATA%\AroniumUtility\gdrive.token.enc` con renovación automática.

Para verificar el estado de la conexión en cualquier momento:

```powershell
aronium-util drive status
aronium-util doctor
```

---

## Paso 4: Sincronizar la Tabla de Inventario y Cuentas de Fiados a Google Drive

### Sincronización Manual Instantánea
```powershell
aronium-util sync
```

Esto actualiza inmediatamente en tu Google Drive (y en la carpeta local sincronizada `%LOCALAPPDATA%\AroniumUtility\GoogleDriveSync\AroniumUtility\`):

1. **`Aronium_Inventario_y_Fiados.xlsx` (Tabla Maestra Unificada Multi-Hoja):**
   - Pestaña **`Resumen_Fiados`**: Lista de todos los clientes con su deuda total histórica, total abonado, **saldo pendiente actual**, cupo disponible, días de mora, estado (`AL_DIA`, `PENDIENTE`, `CUPO_EXCEDIDO`, `EN_MORA`) y anotaciones.
   - Pestaña **`Detalle_Fiados`**: Todas las facturas fiadas con fecha, número de factura, caja (`POS-01`), nombre del cliente, lista detallada de productos comprados, abonos parciales y notas.
   - Pestaña **`Inventario`**: Tabla completa de existencias con SKU, código de barras, categoría, stock actual, stock mínimo, costo, precio de venta, valorización total y alertas (`OK`, `STOCK_BAJO`, `AGOTADO`, `STOCK_NEGATIVO`).
   - Pestañas **`Cliente_<Nombre>`**: Una hoja individual dentro del libro para cada cliente con su estado de cuenta detallado.
2. **Carpetas Individuales por Cliente (`/AroniumUtility/Fiados/Clientes/`):**
   - `<Nombre_Cliente>.xlsx` (Excel individual del cliente).
   - `<Nombre_Cliente>.csv` (CSV compatible con Excel).
   - `<Nombre_Cliente>_Estado_Cuenta.txt` (Resumen listo para enviar por **WhatsApp** al cliente).

### Sincronización Continua Automática en Segundo Plano
Para que `aronium-util` vigile `pos.db` en tiempo real mientras vendes en Aronium Lite:

```powershell
# Ejecutar en ventana actual
aronium-util watch

# O instalar como servicio / tarea automática de Windows al encender el PC
aronium-util service install
aronium-util service start
```

---

## Paso 5: Comandos Rápidos para el Día a Día en la Tienda

```powershell
# Ver cuentas de fiados pendientes (o filtrar por nombre de cliente)
aronium-util fiados list
aronium-util fiados list --customer "Pedro"

# Agregar una anotación o compromiso de pago a un cliente sin tocar pos.db
aronium-util fiados note add "Don Pedro Pérez" "Prometió pagar el viernes de quincena" --promise 2026-11-25

# Asignar un cupo máximo de crédito a un cliente (alerta si se pasa del tope)
aronium-util fiados limit 2 300000

# Generar estado de cuenta listo para copiar y pegar en WhatsApp
aronium-util fiados statement "Don Pedro Pérez"

# Consultar inventario vivo, alertas de stock bajo/vencimientos y kardex
aronium-util inventory list
aronium-util inventory alerts
aronium-util inventory kardex

# Crear un respaldo en caliente cifrado (AES-256-GCM) y verificar su integridad
aronium-util backup create --passphrase "MiClaveSegura2026"
aronium-util backup verify --file "%LOCALAPPDATA%\AroniumUtility\backups\aronium_backup_....db.zip.enc" --passphrase "MiClaveSegura2026"

# Abrir la consola administrativa (TUI en terminal + panel web en http://localhost:8080)
aronium-util admin --serve
```
