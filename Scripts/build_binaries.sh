#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
export PATH="/opt/homebrew/bin:/usr/local/go/bin:${PATH}"

BIN_DIR="${ROOT_DIR}/dist/bin"
CLI_PAYLOAD_FILE="${ROOT_DIR}/cmd/aronium-util-setup/payload/aronium-util.bin"
UI_PAYLOAD_FILE="${ROOT_DIR}/cmd/aronium-util-setup/payload/aronium-utility-ui.bin"
UI_EMBED_DIST="${ROOT_DIR}/cmd/aronium-utility-ui/dist"

rm -rf "${BIN_DIR}"
mkdir -p "${BIN_DIR}/windows-amd64" "${BIN_DIR}/windows-386" "$(dirname "${CLI_PAYLOAD_FILE}")"

cleanup_payload() {
    echo "PLACEHOLDER_PAYLOAD" > "${CLI_PAYLOAD_FILE}"
    echo "PLACEHOLDER_UI_PAYLOAD" > "${UI_PAYLOAD_FILE}"
}
trap cleanup_payload EXIT

echo "[1/6] Preparing Tauri 2 + Svelte 5 + TypeScript Frontend (ui/)..."
if [[ ! -f "${ROOT_DIR}/ui/dist/index.html" || "${FORCE_UI_REBUILD:-0}" == "1" ]]; then
    (
        cd "${ROOT_DIR}/ui"
        if [[ ! -d "node_modules" ]]; then
            npm install --no-audit --no-fund
        fi
        npm run build
    )
fi
rm -rf "${UI_EMBED_DIST}"
mkdir -p "${UI_EMBED_DIST}"
cp -R "${ROOT_DIR}/ui/dist/." "${UI_EMBED_DIST}/"

echo "[2/6] Cross-compiling aronium-util.exe (CLI Engine) & aronium-utility-ui.exe (UI) for Windows x64 (amd64)..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-amd64/aronium-util.exe" "${ROOT_DIR}/cmd/aronium-util"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H=windowsgui" \
    -o "${BIN_DIR}/windows-amd64/aronium-utility-ui.exe" "${ROOT_DIR}/cmd/aronium-utility-ui"

echo "[3/6] Cross-compiling self-contained Windows x64 Unified Setup Installer (aronium-util-setup-x64.exe)..."
cp "${BIN_DIR}/windows-amd64/aronium-util.exe" "${CLI_PAYLOAD_FILE}"
cp "${BIN_DIR}/windows-amd64/aronium-utility-ui.exe" "${UI_PAYLOAD_FILE}"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-amd64/aronium-util-setup-x64.exe" "${ROOT_DIR}/cmd/aronium-util-setup"

echo "[4/6] Cross-compiling aronium-util.exe (CLI Engine) & aronium-utility-ui.exe (UI) for Windows x86 32-bit (386)..."
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-386/aronium-util.exe" "${ROOT_DIR}/cmd/aronium-util"
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath -ldflags="-s -w -H=windowsgui" \
    -o "${BIN_DIR}/windows-386/aronium-utility-ui.exe" "${ROOT_DIR}/cmd/aronium-utility-ui"

echo "[5/6] Cross-compiling self-contained Windows x86 Unified Setup Installer (aronium-util-setup-x86.exe)..."
cp "${BIN_DIR}/windows-386/aronium-util.exe" "${CLI_PAYLOAD_FILE}"
cp "${BIN_DIR}/windows-386/aronium-utility-ui.exe" "${UI_PAYLOAD_FILE}"
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath -ldflags="-s -w" \
    -o "${BIN_DIR}/windows-386/aronium-util-setup-x86.exe" "${ROOT_DIR}/cmd/aronium-util-setup"

echo "[✓] Windows x64 and x86 CLI, Aronium Utility UI & Unified Setup Installers built in ${BIN_DIR}"
