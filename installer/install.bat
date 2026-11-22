@echo off
setlocal EnableDelayedExpansion
chcp 65001 >nul 2>&1

echo ==========================================================================
echo   INSTALADOR UNIFICADO DE ARONIUM UTILITY v1.2.5 (CLI + Aronium Utility UI)
echo ==========================================================================

set "INSTALL_DIR=%LOCALAPPDATA%\Programs\AroniumUtility"
set "DATA_DIR=%LOCALAPPDATA%\AroniumUtility"
set "POS_DB=%LOCALAPPDATA%\Aronium\Data\pos.db"
set "STATE_DB=%DATA_DIR%\aronium_utility_state.db"
set "ACCOUNTS_DB=%DATA_DIR%\cuentas.db"
set "CONFIG_FILE=%DATA_DIR%\aronium-util.yaml"
set "STORE_ID=STORE-01"
set "REGISTER_ID=POS-01"

if not "%~1"=="" set "REGISTER_ID=%~1"
if not "%~2"=="" set "STORE_ID=%~2"

echo [1/5] Creando directorios en "%INSTALL_DIR%" y "%DATA_DIR%"...
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"
if not exist "%DATA_DIR%" mkdir "%DATA_DIR%"
if not exist "%DATA_DIR%\backups" mkdir "%DATA_DIR%\backups"
if not exist "%DATA_DIR%\GoogleDriveSync" mkdir "%DATA_DIR%\GoogleDriveSync"

echo [2/5] Cerrando procesos activos y copiando ejecutables aronium-util.exe y aronium-utility-ui.exe...
taskkill /F /T /IM aronium-utility-ui.exe >nul 2>&1
taskkill /F /T /IM aronium-util.exe >nul 2>&1
sc stop AroniumUtilityService >nul 2>&1
if exist "%DATA_DIR%\cuentas.db" (
    copy /Y "%DATA_DIR%\cuentas.db" "%DATA_DIR%\backups\cuentas_pre_install_snapshot.db" >nul 2>&1
) else if exist "%DATA_DIR%\backups\cuentas_pre_install_snapshot.db" (
    copy /Y "%DATA_DIR%\backups\cuentas_pre_install_snapshot.db" "%DATA_DIR%\cuentas.db" >nul 2>&1
) else if exist "%DATA_DIR%\GoogleDriveSync\AroniumUtility\Cuentas_Credito\cuentas_backup.db" (
    copy /Y "%DATA_DIR%\GoogleDriveSync\AroniumUtility\Cuentas_Credito\cuentas_backup.db" "%DATA_DIR%\cuentas.db" >nul 2>&1
)
if exist "%~dp0aronium-util.exe" (
    copy /Y "%~dp0aronium-util.exe" "%INSTALL_DIR%\aronium-util.exe" >nul
)
if exist "%~dp0aronium-utility-ui.exe" (
    copy /Y "%~dp0aronium-utility-ui.exe" "%INSTALL_DIR%\aronium-utility-ui.exe" >nul
)
if exist "%~dp0app-icon.ico" (
    copy /Y "%~dp0app-icon.ico" "%INSTALL_DIR%\app-icon.ico" >nul
)

echo [3/5] Configurando variables de entorno de Windows y PATH del usuario...
reg add "HKCU\Environment" /v ARONIUM_UTIL_HOME /t REG_EXPAND_SZ /d "%DATA_DIR%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_UTIL_CONFIG /t REG_EXPAND_SZ /d "%CONFIG_FILE%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_POS_DB /t REG_EXPAND_SZ /d "%POS_DB%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_STATE_DB /t REG_EXPAND_SZ /d "%STATE_DB%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_ACCOUNTS_DB /t REG_EXPAND_SZ /d "%ACCOUNTS_DB%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_STORE_ID /t REG_SZ /d "%STORE_ID%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_REGISTER_ID /t REG_SZ /d "%REGISTER_ID%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_CLI_BIN /t REG_EXPAND_SZ /d "%INSTALL_DIR%\aronium-util.exe" /f >nul
reg add "HKCU\Environment" /v ARONIUM_UI_BIN /t REG_EXPAND_SZ /d "%INSTALL_DIR%\aronium-utility-ui.exe" /f >nul

powershell -NoProfile -NonInteractive -Command "$dir = '%INSTALL_DIR%'; $cur = [Environment]::GetEnvironmentVariable('Path', 'User'); if (-not $cur) { $cur = '' }; $parts = $cur -split ';' | Where-Object { $_ -ne '' }; if ($parts -notcontains $dir) { [Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User') }"

echo [4/5] Inicializando configuracion aronium-util.yaml y base de cuentas (Caja: %REGISTER_ID%)...
"%INSTALL_DIR%\aronium-util.exe" init --config "%CONFIG_FILE%" --pos-db "%POS_DB%" --accounts-db "%ACCOUNTS_DB%" --store-id "%STORE_ID%" --register "%REGISTER_ID%"

echo [5/5] Registrando arranque automatico del CLI (Startup .lnk) al encender Windows...
"%INSTALL_DIR%\aronium-util.exe" service install --config "%CONFIG_FILE%"

echo.
echo ==========================================================================
echo [OK] Aronium Utility (CLI + UI) instalado y variables configuradas.
echo   - Ejecutable CLI: %INSTALL_DIR%\aronium-util.exe
echo   - Ejecutable UI:  %INSTALL_DIR%\aronium-utility-ui.exe
echo   - Puedes lanzar la interfaz grafica ejecutando:
echo       aronium-utility-ui   (o desde el CLI: aronium-util ui)
echo ==========================================================================
endlocal
