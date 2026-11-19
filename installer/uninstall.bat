@echo off
setlocal
echo Desinstalando Aronium Utility y limpiando variables de entorno...

set "INSTALL_DIR=%LOCALAPPDATA%\Programs\AroniumUtility"

reg delete "HKCU\Environment" /v ARONIUM_UTIL_HOME /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_UTIL_CONFIG /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_POS_DB /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_STATE_DB /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_STORE_ID /f >nul 2>&1
reg delete "HKCU\Environment" /v ARONIUM_REGISTER_ID /f >nul 2>&1

if exist "%INSTALL_DIR%\aronium-util.exe" del /F /Q "%INSTALL_DIR%\aronium-util.exe"
echo [OK] Aronium Utility desinstalado.
endlocal
