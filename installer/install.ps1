param(
    [string]$RegisterId = "POS-01",
    [string]$StoreId = "TIENDA-01",
    [string]$PosDbPath = "$env:LOCALAPPDATA\Aronium\Data\pos.db",
    [switch]$InstallService = $false,
    [switch]$ConnectGoogleDrive = $false
)

$ErrorActionPreference = "Stop"

Write-Host "======================================================================" -ForegroundColor Cyan
Write-Host "  INSTALADOR DE ARONIUM UTILITY v1.0.0 (PowerShell)" -ForegroundColor Cyan
Write-Host "======================================================================" -ForegroundColor Cyan

$InstallDir = Join-Path $env:LOCALAPPDATA "Programs\AroniumUtility"
$DataDir    = Join-Path $env:LOCALAPPDATA "AroniumUtility"
$ConfigFile = Join-Path $DataDir "aronium-util.yaml"
$StateDb    = Join-Path $DataDir "aronium_utility_state.db"

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir "backups") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $DataDir "GoogleDriveSync") | Out-Null

$SourceBin = Join-Path $PSScriptRoot "aronium-util.exe"
if (-not (Test-Path $SourceBin)) {
    $SourceBin = Join-Path $PSScriptRoot "aronium-util-windows-amd64.exe"
}
$DestBin = Join-Path $InstallDir "aronium-util.exe"
Copy-Item -Path $SourceBin -Destination $DestBin -Force

# Set User Environment Variables
[Environment]::SetEnvironmentVariable("ARONIUM_UTIL_HOME",   $DataDir,    "User")
[Environment]::SetEnvironmentVariable("ARONIUM_UTIL_CONFIG", $ConfigFile, "User")
[Environment]::SetEnvironmentVariable("ARONIUM_POS_DB",      $PosDbPath,  "User")
[Environment]::SetEnvironmentVariable("ARONIUM_STATE_DB",    $StateDb,    "User")
[Environment]::SetEnvironmentVariable("ARONIUM_STORE_ID",    $StoreId,    "User")
[Environment]::SetEnvironmentVariable("ARONIUM_REGISTER_ID", $RegisterId, "User")

# Add to User PATH
$CurrentPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $CurrentPath) { $CurrentPath = "" }
$Parts = $CurrentPath -split ";" | Where-Object { $_ -ne "" }
if ($Parts -notcontains $InstallDir) {
    $NewPath = ($Parts + $InstallDir) -join ";"
    [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
}
$env:Path = "$env:Path;$InstallDir"

& $DestBin init --config $ConfigFile --pos-db $PosDbPath --store-id $StoreId --register $RegisterId

if ($InstallService) {
    & $DestBin service install --config $ConfigFile
}

if ($ConnectGoogleDrive) {
    & $DestBin drive login --config $ConfigFile
}

Write-Host "[OK] Aronium Utility instalado en $DestBin y variables de entorno configuradas." -ForegroundColor Green
