#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
export PATH="/opt/homebrew/bin:/usr/local/go/bin:${PATH}"

BIN_DIR="${ROOT_DIR}/dist/bin"
CLI_PAYLOAD_FILE="${ROOT_DIR}/cmd/aronium-util-setup/payload/aronium-util.bin"
UI_PAYLOAD_FILE="${ROOT_DIR}/cmd/aronium-util-setup/payload/aronium-utility-ui.bin"
UNINST_PAYLOAD_FILE="${ROOT_DIR}/cmd/aronium-util-setup/payload/aronium-util-uninstall.bin"
CERT_PAYLOAD_FILE="${ROOT_DIR}/cmd/aronium-util-setup/payload/AroniumUtility-CodeSigning.cer"
UI_EMBED_DIST="${ROOT_DIR}/cmd/aronium-utility-ui/dist"

rm -rf "${BIN_DIR}"
mkdir -p "${BIN_DIR}/windows-amd64" "${BIN_DIR}/windows-386" "$(dirname "${CLI_PAYLOAD_FILE}")"

cleanup_payload() {
    echo "PLACEHOLDER_PAYLOAD" > "${CLI_PAYLOAD_FILE}"
    echo "PLACEHOLDER_UI_PAYLOAD" > "${UI_PAYLOAD_FILE}"
    echo "PLACEHOLDER_UNINSTALL_PAYLOAD" > "${UNINST_PAYLOAD_FILE}"
    echo "PLACEHOLDER_CERT" > "${CERT_PAYLOAD_FILE}"
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

echo "[2a/6] Generating Windows .ico icon & COFF PE resources (.syso)..."
(cd "${ROOT_DIR}" && go run ./Scripts/gen_winres.go)

CERT_DIR="/tmp/AroniumUtilityCert"
CERT_KEY="${CERT_DIR}/AroniumUtility-CodeSigning.key"
CERT_CER="${BIN_DIR}/AroniumUtility-CodeSigning.cer"
CERT_PFX="${CERT_DIR}/AroniumUtility-CodeSigning.pfx"
CERT_PASS="AroniumUtility2026"
rm -rf "${CERT_DIR}"
mkdir -p "${CERT_DIR}" "${BIN_DIR}"

if command -v openssl >/dev/null 2>&1 && command -v osslsigncode >/dev/null 2>&1; then
    echo "[2b/6] Generating SHA-256 Authenticode code-signing certificate..."
    openssl req -x509 -newkey rsa:4096 -sha256 -nodes -days 1825 \
        -keyout "${CERT_KEY}" -out "${CERT_CER}" \
        -subj "/CN=SilverFeit Aronium Utility/O=SilverFeit/C=CO" \
        -addext "basicConstraints=critical,CA:FALSE" \
        -addext "keyUsage=critical,digitalSignature" \
        -addext "extendedKeyUsage=codeSigning" >/dev/null 2>&1
    openssl pkcs12 -export -out "${CERT_PFX}" -inkey "${CERT_KEY}" -in "${CERT_CER}" \
        -passout "pass:${CERT_PASS}" -name "SilverFeit Aronium Utility" >/dev/null 2>&1 || \
    openssl pkcs12 -export -legacy -out "${CERT_PFX}" -inkey "${CERT_KEY}" -in "${CERT_CER}" \
        -passout "pass:${CERT_PASS}" -name "SilverFeit Aronium Utility" >/dev/null 2>&1
fi

sign_win_exe() {
    local target="$1"
    local label="$2"
    if [[ ! -f "${CERT_PFX}" ]] || ! command -v osslsigncode >/dev/null 2>&1; then
        echo "[warn] osslsigncode or Authenticode cert unavailable; skipping signature for ${target}"
        return 0
    fi
    local tmp_signed="${target}.signed"
    rm -f "${tmp_signed}"
    osslsigncode sign \
        -pkcs12 "${CERT_PFX}" \
        -pass "${CERT_PASS}" \
        -n "${label}" \
        -i "https://github.com/SilverFeit/Aronium-Utility" \
        -h sha256 \
        -in "${target}" \
        -out "${tmp_signed}" >/dev/null
    mv "${tmp_signed}" "${target}"
    osslsigncode verify -in "${target}" -CAfile "${CERT_CER}" >/dev/null
    echo "  [signed SHA-256] ${target##${ROOT_DIR}/}"
}

echo "[2/6] Cross-compiling aronium-util.exe (CLI), aronium-utility-ui.exe (UI) & aronium-util-uninstall-x64.exe for Windows x64..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -o "${BIN_DIR}/windows-amd64/aronium-util.exe" "${ROOT_DIR}/cmd/aronium-util"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-H=windowsgui" \
    -o "${BIN_DIR}/windows-amd64/aronium-utility-ui.exe" "${ROOT_DIR}/cmd/aronium-utility-ui"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -o "${BIN_DIR}/windows-amd64/aronium-util-uninstall-x64.exe" "${ROOT_DIR}/cmd/aronium-util-uninstall"
sign_win_exe "${BIN_DIR}/windows-amd64/aronium-util.exe" "Aronium Utility CLI"
sign_win_exe "${BIN_DIR}/windows-amd64/aronium-utility-ui.exe" "Aronium Utility UI"
sign_win_exe "${BIN_DIR}/windows-amd64/aronium-util-uninstall-x64.exe" "Aronium Utility Uninstaller x64"

echo "[3/6] Cross-compiling self-contained Windows x64 Unified Setup Installer (aronium-util-setup-x64.exe)..."
cp "${BIN_DIR}/windows-amd64/aronium-util.exe" "${CLI_PAYLOAD_FILE}"
cp "${BIN_DIR}/windows-amd64/aronium-utility-ui.exe" "${UI_PAYLOAD_FILE}"
cp "${BIN_DIR}/windows-amd64/aronium-util-uninstall-x64.exe" "${UNINST_PAYLOAD_FILE}"
[[ -f "${CERT_CER}" ]] && cp "${CERT_CER}" "${CERT_PAYLOAD_FILE}"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -o "${BIN_DIR}/windows-amd64/aronium-util-setup-x64.exe" "${ROOT_DIR}/cmd/aronium-util-setup"
sign_win_exe "${BIN_DIR}/windows-amd64/aronium-util-setup-x64.exe" "Aronium Utility Unified Installer x64"

echo "[4/6] Cross-compiling aronium-util.exe, aronium-utility-ui.exe & aronium-util-uninstall-x86.exe for Windows x86..."
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath \
    -o "${BIN_DIR}/windows-386/aronium-util.exe" "${ROOT_DIR}/cmd/aronium-util"
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath -ldflags="-H=windowsgui" \
    -o "${BIN_DIR}/windows-386/aronium-utility-ui.exe" "${ROOT_DIR}/cmd/aronium-utility-ui"
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath \
    -o "${BIN_DIR}/windows-386/aronium-util-uninstall-x86.exe" "${ROOT_DIR}/cmd/aronium-util-uninstall"
sign_win_exe "${BIN_DIR}/windows-386/aronium-util.exe" "Aronium Utility CLI (32-bit)"
sign_win_exe "${BIN_DIR}/windows-386/aronium-utility-ui.exe" "Aronium Utility UI (32-bit)"
sign_win_exe "${BIN_DIR}/windows-386/aronium-util-uninstall-x86.exe" "Aronium Utility Uninstaller x86"

echo "[5/6] Cross-compiling self-contained Windows x86 Unified Setup Installer (aronium-util-setup-x86.exe)..."
cp "${BIN_DIR}/windows-386/aronium-util.exe" "${CLI_PAYLOAD_FILE}"
cp "${BIN_DIR}/windows-386/aronium-utility-ui.exe" "${UI_PAYLOAD_FILE}"
cp "${BIN_DIR}/windows-386/aronium-util-uninstall-x86.exe" "${UNINST_PAYLOAD_FILE}"
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -trimpath \
    -o "${BIN_DIR}/windows-386/aronium-util-setup-x86.exe" "${ROOT_DIR}/cmd/aronium-util-setup"
sign_win_exe "${BIN_DIR}/windows-386/aronium-util-setup-x86.exe" "Aronium Utility Unified Installer x86"

rm -rf "${CERT_DIR}"
echo "[✓] Windows x64 and x86 CLI, Aronium Utility UI & Unified Setup Installers built in ${BIN_DIR}"
