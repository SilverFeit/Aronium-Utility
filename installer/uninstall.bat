@echo off
setlocal
echo Desinstalando Aronium Utility (CLI + UI) y limpiando variables de entorno...

set "INSTALL_DIR=%LOCALAPPDATA%\Programs\AroniumUtility"
set "DATA_DIR=%LOCALAPPDATA%\AroniumUtility"

taskkill /F /T /IM aronium-utility-ui.exe >nul 2>&1
taskkill /F /T /IM aronium-util.exe >nul 2>&1
sc stop AroniumUtilityService >nul 2>&1
if exist "%DATA_DIR%\cuentas.db" (
    if not exist "%DATA_DIR%\backups" mkdir "%DATA_DIR%\backups"
    copy /Y "%DATA_DIR%\cuentas.db" "%DATA_DIR%\backups\cuentas_pre_install_snapshot.db" >nul 2>&1
)

reg delete "HKCU\Environment" /v ARONIUM_UTIL_HOME /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_UTIL_CONFIG /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_POS_DB /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_STATE_DB /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_ACCOUNTS_DB /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_STORE_ID /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_REGISTER_ID /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_CLI_BIN /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_UI_BIN /f >nul 2>&1
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v AroniumUtilityService /f >nul 2>&1
if exist "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility Watcher.lnk" del /F /Q "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility Watcher.lnk"

if exist "%INSTALL_DIR%\aronium-util.exe" del /F /Q "%INSTALL_DIR%\aronium-util.exe"
if exist "%INSTALL_DIR%\aronium-utility-ui.exe" del /F /Q "%INSTALL_DIR%\aronium-utility-ui.exe"
if exist "%INSTALL_DIR%\app-icon.ico" del /F /Q "%INSTALL_DIR%\app-icon.ico"
echo [OK] Aronium Utility (CLI + UI) desinstalado. Bases de datos y cuentas preservadas intactas en "%DATA_DIR%".
endlocal
