#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
export PATH="/opt/homebrew/bin:/usr/local/go/bin:${PATH}"

DEFAULT_VER="$(tr -d '[:space:]' < "${ROOT_DIR}/VERSION" 2>/dev/null || echo "1.0.0")"
VERSION="${1:-${DEFAULT_VER}}"

"${SCRIPT_DIR}/build_binaries.sh"

BIN_DIR="${ROOT_DIR}/dist/bin"
RELEASE_DIR="${ROOT_DIR}/dist/release"
STAGE_DIR="/tmp/AroniumUtilityReleaseStage"

rm -rf "${RELEASE_DIR}" "${STAGE_DIR}"
mkdir -p "${RELEASE_DIR}" "${STAGE_DIR}/x64" "${STAGE_DIR}/x86"

# Assemble Windows x64 Package
cp "${BIN_DIR}/windows-amd64/aronium-util.exe" "${STAGE_DIR}/x64/"
cp "${BIN_DIR}/windows-amd64/aronium-util-setup-x64.exe" "${STAGE_DIR}/x64/"
cp "${ROOT_DIR}/installer/install.bat" "${STAGE_DIR}/x64/"
cp "${ROOT_DIR}/installer/install.ps1" "${STAGE_DIR}/x64/"
cp "${ROOT_DIR}/installer/uninstall.bat" "${STAGE_DIR}/x64/"
cp "${ROOT_DIR}/README.md" "${STAGE_DIR}/x64/"
cp "${ROOT_DIR}/LICENSE" "${STAGE_DIR}/x64/"

# Assemble Windows x86 (32-bit) Package
cp "${BIN_DIR}/windows-386/aronium-util.exe" "${STAGE_DIR}/x86/"
cp "${BIN_DIR}/windows-386/aronium-util-setup-x86.exe" "${STAGE_DIR}/x86/"
cp "${ROOT_DIR}/installer/install.bat" "${STAGE_DIR}/x86/"
cp "${ROOT_DIR}/installer/install.ps1" "${STAGE_DIR}/x86/"
cp "${ROOT_DIR}/installer/uninstall.bat" "${STAGE_DIR}/x86/"
cp "${ROOT_DIR}/README.md" "${STAGE_DIR}/x86/"
cp "${ROOT_DIR}/LICENSE" "${STAGE_DIR}/x86/"

ZIP_X64="Aronium-Utility-${VERSION}-windows-x64.zip"
ZIP_X86="Aronium-Utility-${VERSION}-windows-x86.zip"

echo "[5/6] Packaging release archives and standalone installers..."
(cd "${STAGE_DIR}/x64" && zip -q -r "${RELEASE_DIR}/${ZIP_X64}" .)
(cd "${STAGE_DIR}/x86" && zip -q -r "${RELEASE_DIR}/${ZIP_X86}" .)

cp "${BIN_DIR}/windows-amd64/aronium-util-setup-x64.exe" "${RELEASE_DIR}/aronium-util-setup-x64.exe"
cp "${BIN_DIR}/windows-386/aronium-util-setup-x86.exe" "${RELEASE_DIR}/aronium-util-setup-x86.exe"

rm -rf "${STAGE_DIR}"

echo "[6/6] Computing SHA256SUMS.txt..."
(
    cd "${RELEASE_DIR}"
    if command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "${ZIP_X64}" "${ZIP_X86}" "aronium-util-setup-x64.exe" "aronium-util-setup-x86.exe" > SHA256SUMS.txt
    else
        sha256sum "${ZIP_X64}" "${ZIP_X86}" "aronium-util-setup-x64.exe" "aronium-util-setup-x86.exe" > SHA256SUMS.txt
    fi
)

echo "Release artifacts ready in ${RELEASE_DIR}:"
cat "${RELEASE_DIR}/SHA256SUMS.txt"
