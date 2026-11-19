param(
    [string]$RegisterId = "POS-01",
    [string]$StoreId = "STORE-01",
    [string]$PosDbPath = "$env:LOCALAPPDATA\Aronium\Data\pos.db",
    [switch]$InstallService = $true,
    [switch]$ConnectGoogleDrive = $false,
    [switch]$LaunchUI = $false
)

$ErrorActionPreference = "Stop"

Write-Host "==========================================================================" -ForegroundColor Cyan
Write-Host "  INSTALADOR DE ARONIUM UTILITY v1.1.0 (CLI en Go + Aronium Utility UI)" -ForegroundColor Cyan
Write-Host "==========================================================================" -ForegroundColor Cyan

$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\AroniumUtility"
$DataDir    = Join-Path $env:LOCALAPPDATA "AroniumUtility"
$ConfigFile = Join-Path $DataDir "aronium-util.yaml"
$StateDb    = Join-Path $DataDir "aronium_utility_state.db"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir "backups") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir "GoogleDriveSync") | Out-Null

$DestCLIBin = Join-Path $InstallDir "aronium-util.exe"
$DestUIBin  = Join-Path $InstallDir "aronium-utility-ui.exe"

Copy-Item -Path (Join-Path $PSScriptRoot "aronium-util.exe") -Destination $DestCLIBin -Force
if (Test-Path (Join-Path $PSScriptRoot "aronium-utility-ui.exe")) {
    Copy-Item -Path (Join-Path $PSScriptRoot "aronium-utility-ui.exe") -Destination $DestUIBin -Force
}

[Environment]::SetEnvironmentVariable("ARONIUM_UTIL_HOME",   $DataDir,    "User")
[Environment]::SetEnvironmentVariable("ARONIUM_UTIL_CONFIG", $ConfigFile, "User")
[Environment]::SetEnvironmentVariable("ARONIUM_POS_DB",      $PosDbPath,  "User")
[Environment]::SetEnvironmentVariable("ARONIUM_STATE_DB",    $StateDb,    "User")
[Environment]::SetEnvironmentVariable("ARONIUM_STORE_ID",    $StoreId,    "User")
[Environment]::SetEnvironmentVariable("ARONIUM_REGISTER_ID", $RegisterId, "User")
[Environment]::SetEnvironmentVariable("ARONIUM_CLI_BIN",     $DestCLIBin, "User")
[Environment]::SetEnvironmentVariable("ARONIUM_UI_BIN",      $DestUIBin,  "User")

$CurrentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $CurrentPath) { $CurrentPath = "" }
$Parts = $CurrentPath -split ";" | Where-Object { $_ -ne "" }
if ($Parts -notcontains $InstallDir) {
    $NewPath = ($Parts + $InstallDir) -join ";"
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
}
$env:Path = "$env:Path;$InstallDir"

& $DestCLIBin init --config $ConfigFile --pos-db $PosDbPath --store-id $StoreId --register $RegisterId

if ($InstallService) {
    & $DestCLIBin service install --config $ConfigFile
}

if ($ConnectGoogleDrive) {
    & $DestCLIBin drive login --config $ConfigFile
}

if ($LaunchUI -and (Test-Path $DestUIBin)) {
    Start-Process -FilePath $DestUIBin
}

Write-Host "[OK] Aronium Utility (CLI + UI) instalado en $InstallDir y variables de entorno configuradas." -ForegroundColor Green
