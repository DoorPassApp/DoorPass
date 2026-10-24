#!/usr/bin/env bash
# Builds doorpass-helper for every target, then the Electron installers:
#   macOS arm64 + x64 DMGs, Windows x64 NSIS installer.
# Usage: desktop/build.sh [mac|win|all]   (default: all)
#
# Needs: Go (version from go.mod), Node + npm, and for Windows
# x86_64-w64-mingw32-gcc (brew install mingw-w64).
# Set RELEASE_DIR to collect installers elsewhere; it defaults to the Lexar
# drive when mounted. Build output stays on the internal disk because exFAT
# can't hold the symlinks inside .app bundles.
set -euo pipefail

TARGET="${1:-all}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="${ROOT}/desktop/app"
VERSION="$(node -p "require('${APP}/package.json').version")"
LDFLAGS="-s -w -X main.version=${VERSION}"
WINTUN_VERSION="0.14.1"
WINTUN_SHA256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"

LEXAR="/Volumes/Lexar/DoorPass"
if [[ -z "${RELEASE_DIR:-}" && -d "/Volumes/Lexar" ]]; then
  RELEASE_DIR="${LEXAR}/releases/${VERSION}"
  export ELECTRON_CACHE="${LEXAR}/cache/electron"
fi

build_helper() { # GOOS GOARCH OUTDIR [CC]
  local out="${APP}/helper/$3"
  mkdir -p "${out}"
  local exe="doorpass-helper"
  [[ "$1" == "windows" ]] && exe="doorpass-helper.exe"
  echo "==> doorpass-helper ${VERSION} for $1/$2"
  (cd "${ROOT}" && GOOS="$1" GOARCH="$2" CGO_ENABLED=1 CC="${4:-}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${out}/${exe}" ./desktop/helper)
}

fetch_wintun() {
  local out="${APP}/helper/win-x64"
  [[ -f "${out}/wintun.dll" ]] && return
  local tmp
  tmp="$(mktemp -d)"
  echo "==> Wintun ${WINTUN_VERSION}"
  curl -fsSL -o "${tmp}/wintun.zip" "https://www.wintun.net/builds/wintun-${WINTUN_VERSION}.zip"
  echo "${WINTUN_SHA256}  ${tmp}/wintun.zip" | shasum -a 256 -c -
  unzip -q "${tmp}/wintun.zip" -d "${tmp}"
  cp "${tmp}/wintun/bin/amd64/wintun.dll" "${out}/"
  cp "${tmp}/wintun/LICENSE.txt" "${out}/wintun-LICENSE.txt"
  rm -rf "${tmp}"
}

(cd "${ROOT}" && go vet ./desktop/helper && go test ./desktop/helper)
(cd "${APP}" && npm ci --no-audit --no-fund)

if [[ "${TARGET}" == "mac" || "${TARGET}" == "all" ]]; then
  build_helper darwin arm64 mac-arm64
  build_helper darwin amd64 mac-x64
  (cd "${APP}" && npm run dist:mac)
fi
if [[ "${TARGET}" == "win" || "${TARGET}" == "all" ]]; then
  build_helper windows amd64 win-x64 x86_64-w64-mingw32-gcc
  fetch_wintun
  (cd "${APP}" && npm run dist:win)
fi

if [[ -n "${RELEASE_DIR:-}" ]]; then
  mkdir -p "${RELEASE_DIR}"
  cp "${APP}"/dist/DoorPass-"${VERSION}"-* "${RELEASE_DIR}/"
  rm -f "${RELEASE_DIR}"/*.blockmap
  (cd "${RELEASE_DIR}" && shasum -a 256 DoorPass-* > SHA256SUMS.txt)
  rm -f "${RELEASE_DIR}"/._* # macOS AppleDouble files on exFAT
  rm -rf "${APP}/dist"
  echo "==> Installers in ${RELEASE_DIR}"
else
  echo "==> Installers in ${APP}/dist"
fi
