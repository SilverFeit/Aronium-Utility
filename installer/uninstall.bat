@echo off
setlocal EnableDelayedExpansion
chcp 65001 >nul 2>&1

set "SCRIPT_DIR=%~dp0"
if exist "%SCRIPT_DIR%aronium-util-uninstall-x64.exe" (
    "%SCRIPT_DIR%aronium-util-uninstall-x64.exe" %*
    exit /b %ERRORLEVEL%
)
if exist "%SCRIPT_DIR%aronium-util-uninstall-x86.exe" (
    "%SCRIPT_DIR%aronium-util-uninstall-x86.exe" %*
    exit /b %ERRORLEVEL%
)
if exist "%LOCALAPPDATA%\Programs\AroniumUtility\aronium-util-uninstall.exe" (
    "%LOCALAPPDATA%\Programs\AroniumUtility\aronium-util-uninstall.exe" %*
    exit /b %ERRORLEVEL%
)

echo ==========================================================================
echo   DESINSTALADOR COMPLETO DE ARONIUM UTILITY (LIMPIEZA 100%%)
echo ==========================================================================

set "INSTALL_DIR=%LOCALAPPDATA%\Programs\AroniumUtility"
set "DATA_DIR=%LOCALAPPDATA%\AroniumUtility"

taskkill /F /T /IM aronium-utility-ui.exe >nul 2>&1
taskkill /F /T /IM aronium-util.exe >nul 2>&1
sc stop AroniumUtilityService >nul 2>&1
sc delete AroniumUtilityService >nul 2>&1
schtasks /End /TN AroniumUtilityBackgroundWatcher >nul 2>&1
schtasks /Delete /TN AroniumUtilityBackgroundWatcher /F >nul 2>&1

for /f "usebackq delims=" %%D in (`powershell -NoProfile -Command "[Environment]::GetFolderPath('MyDocuments')"`) do set "DOCS_DIR=%%D"
if "%DOCS_DIR%"=="" set "DOCS_DIR=%USERPROFILE%\Documents"
set "BACKUP_DIR=%DOCS_DIR%\AroniumUtility_Respaldo_Cuentas"

if exist "%DATA_DIR%\cuentas.db" (
    if not exist "%BACKUP_DIR%" mkdir "%BACKUP_DIR%"
    copy /Y "%DATA_DIR%\cuentas.db" "%BACKUP_DIR%\cuentas.db" >nul 2>&1
    echo [OK] Copia de seguridad de cuentas.db guardada en: "%BACKUP_DIR%\cuentas.db"
)

for %%K in (ARONIUM_UTIL_HOME ARONIUM_UTIL_CONFIG ARONIUM_POS_DB ARONIUM_STATE_DB ARONIUM_ACCOUNTS_DB ARONIUM_STORE_ID ARONIUM_REGISTER_ID ARONIUM_CLI_BIN ARONIUM_UI_BIN ARONIUM_POS_DB_PASSWORD ARONIUM_TIMEZONE) do (
    reg delete "HKCU\Environment" /v %%K /f >nul 2>&1
    reg delete "HKLM\SYSTEM\CurrentControlSet\Control\Session Manager\Environment" /v %%K /f >nul 2>&1
)
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v AroniumUtilityUI /f >nul 2>&1
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v AroniumUtilityService /f >nul 2>&1

if exist "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility.lnk" del /F /Q "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility.lnk"
if exist "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility Watcher.lnk" del /F /Q "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Aronium Utility Watcher.lnk"
if exist "%USERPROFILE%\Desktop\Aronium Utility UI.lnk" del /F /Q "%USERPROFILE%\Desktop\Aronium Utility UI.lnk"
if exist "%WINDIR%\aronium-util.cmd" del /F /Q "%WINDIR%\aronium-util.cmd" >nul 2>&1
if exist "%WINDIR%\aronium-utility-ui.cmd" del /F /Q "%WINDIR%\aronium-utility-ui.cmd" >nul 2>&1
if exist "%WINDIR%\aronium-util-uninstall.cmd" del /F /Q "%WINDIR%\aronium-util-uninstall.cmd" >nul 2>&1
if exist "%WINDIR%\System32\aronium-util.exe" del /F /Q "%WINDIR%\System32\aronium-util.exe" >nul 2>&1

if exist "%INSTALL_DIR%" rmdir /S /Q "%INSTALL_DIR%" >nul 2>&1
if exist "%DATA_DIR%" rmdir /S /Q "%DATA_DIR%" >nul 2>&1

echo [OK] Desinstalacion completa finalizada.
endlocal
