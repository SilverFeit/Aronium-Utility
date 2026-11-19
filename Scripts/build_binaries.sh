#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
export PATH="/opt/homebrew/bin:/usr/local/go/bin:${PATH}"

BIN_DIR="${ROOT_DIR}/dist/bin"
PAYLOAD_FILE="${ROOT_DIR}/cmd/aronium-util-setup/payload/aronium-util.bin"

rm -rf "${BIN_DIR}"
mkdir -p "${BIN_DIR}/windows-amd64" "${BIN_DIR}/windows-386" "$(dirname "${PAYLOAD_FILE}")"

cleanup_payload() {
    echo "PLACEHOLDER_PAYLOAD" > "${PAYLOAD_FILE}"
}
trap cleanup_payload EXIT

echo "[1/4] Cross-compiling aronium-util.exe for Windows x64 (amd64)..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-amd64/aronium-util.exe" "${ROOT_DIR}/cmd/aronium-util"

echo "[2/4] Cross-compiling self-contained Windows x64 Setup Installer (aronium-util-setup-x64.exe)..."
cp "${BIN_DIR}/windows-amd64/aronium-util.exe" "${PAYLOAD_FILE}"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-amd64/aronium-util-setup-x64.exe" "${ROOT_DIR}/cmd/aronium-util-setup"

echo "[3/4] Cross-compiling aronium-util.exe for Windows x86 32-bit (386)..."
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-386/aronium-util.exe" "${ROOT_DIR}/cmd/aronium-util"

echo "[4/4] Cross-compiling self-contained Windows x86 Setup Installer (aronium-util-setup-x86.exe)..."
cp "${BIN_DIR}/windows-386/aronium-util.exe" "${PAYLOAD_FILE}"
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-386/aronium-util-setup-x86.exe" "${ROOT_DIR}/cmd/aronium-util-setup"

echo "[✓] Windows x64 and x86 CLI binaries & Setup Installers built in ${BIN_DIR}"
