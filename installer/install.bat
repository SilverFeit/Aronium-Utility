@echo off
setlocal EnableDelayedExpansion
chcp 65001 >nul 2>&1

echo ======================================================================
echo   INSTALADOR SENCILLO DE ARONIUM UTILITY v1.0.0 (Windows CLI + PATH)
echo ======================================================================

set "INSTALL_DIR=%LOCALAPPDATA%\Programs\AroniumUtility"
set "DATA_DIR=%LOCALAPPDATA%\AroniumUtility"
set "POS_DB=%LOCALAPPDATA%\Aronium\Data\pos.db"
set "STATE_DB=%DATA_DIR%\aronium_utility_state.db"
set "CONFIG_FILE=%DATA_DIR%\aronium-util.yaml"
set "STORE_ID=TIENDA-01"
set "REGISTER_ID=POS-01"

if not "%~1"=="" set "REGISTER_ID=%~1"
if not "%~2"=="" set "STORE_ID=%~2"

echo [1/4] Creando directorios en "%INSTALL_DIR%" y "%DATA_DIR%"...
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"
if not exist "%DATA_DIR%" mkdir "%DATA_DIR%"
if not exist "%DATA_DIR%\backups" mkdir "%DATA_DIR%\backups"
if not exist "%DATA_DIR%\GoogleDriveSync" mkdir "%DATA_DIR%\GoogleDriveSync"

echo [2/4] Copiando ejecutable aronium-util.exe...
if exist "%~dp0aronium-util.exe" (
    copy /Y "%~dp0aronium-util.exe" "%INSTALL_DIR%\aronium-util.exe" >nul
) else if exist "%~dp0aronium-util-windows-amd64.exe" (
    copy /Y "%~dp0aronium-util-windows-amd64.exe" "%INSTALL_DIR%\aronium-util.exe" >nul
) else if exist "%~dp0aronium-util-setup-x64.exe" (
    "%~dp0aronium-util-setup-x64.exe" --silent --register "%REGISTER_ID%" --store-id "%STORE_ID%"
    goto :EOF
) else (
    echo [ERROR] No se encontro aronium-util.exe junto al instalador.
    exit /b 1
)

echo [3/4] Configurando variables de entorno de Windows y PATH del usuario...
reg add "HKCU\Environment" /v ARONIUM_UTIL_HOME /t REG_EXPAND_SZ /d "%DATA_DIR%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_UTIL_CONFIG /t REG_EXPAND_SZ /d "%CONFIG_FILE%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_POS_DB /t REG_EXPAND_SZ /d "%POS_DB%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_STATE_DB /t REG_EXPAND_SZ /d "%STATE_DB%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_STORE_ID /t REG_SZ /d "%STORE_ID%" /f >nul
reg add "HKCU\Environment" /v ARONIUM_REGISTER_ID /t REG_SZ /d "%REGISTER_ID%" /f >nul

powershell -NoProfile -NonInteractive -Command "$dir = '%INSTALL_DIR%'; $cur = [Environment]::GetEnvironmentVariable('Path', 'User'); if (-not $cur) { $cur = '' }; $parts = $cur -split ';' | Where-Object { $_ -ne '' }; if ($parts -notcontains $dir) { [Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User') }"

echo [4/4] Inicializando configuracion aronium-util.yaml (Caja: %REGISTER_ID%)...
"%INSTALL_DIR%\aronium-util.exe" init --config "%CONFIG_FILE%" --pos-db "%POS_DB%" --store-id "%STORE_ID%" --register "%REGISTER_ID%"

echo.
echo ======================================================================
echo [OK] Aronium Utility instalado y variables de entorno configuradas.
echo   - Binario CLI:   %INSTALL_DIR%\aronium-util.exe
echo   - Configuracion: %CONFIG_FILE%
echo   - Para vincular Google Drive via Web ejecuta:
echo       aronium-util drive login
echo ======================================================================
endlocal
