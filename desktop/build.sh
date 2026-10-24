#!/usr/bin/env bash
# Builds DoorPass: one universal macOS DMG (Apple silicon + Intel) and the
# Windows x64 installer.
# Usage: desktop/build.sh [mac|win|all]   (default: all)
#
# Needs: Go (version from go.mod), the Xcode Command Line Tools (clang, lipo,
# codesign, iconutil, hdiutil), and for Windows x86_64-w64-mingw32-gcc
# (brew install mingw-w64) for Intra's C networking code, plus makensis or,
# failing that, Docker (OrbStack) to run Debian's nsis package.
# Installers go to the Lexar drive when mounted, else desktop/dist.
set -euo pipefail

TARGET="${1:-all}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DESKTOP="${ROOT}/desktop"
APP="${DESKTOP}/app"
PKG="${DESKTOP}/packaging"
BUILD="${DESKTOP}/build"
VERSION="$(tr -d '[:space:]' < "${DESKTOP}/VERSION")"
LDFLAGS="-s -w -X main.version=${VERSION}"
WINTUN_VERSION="0.14.1"
WINTUN_SHA256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
GOWINRES="github.com/tc-hib/go-winres@v0.3.3"

if [[ -z "${RELEASE_DIR:-}" ]]; then
  if [[ -d "/Volumes/Lexar" ]]; then
    RELEASE_DIR="/Volumes/Lexar/DoorPass/releases/${VERSION}"
  else
    RELEASE_DIR="${DESKTOP}/dist/${VERSION}"
  fi
fi

# The macOS objects must target the same minimum version as Info.plist;
# Go 1.27 itself needs macOS 13.
MAC_ENV=(MACOSX_DEPLOYMENT_TARGET=13.0 CGO_CFLAGS=-mmacosx-version-min=13.0 CGO_LDFLAGS=-mmacosx-version-min=13.0)

build_helper() { # GOOS GOARCH OUTDIR [CC]
  local out="${APP}/helper/$3" exe="doorpass-helper"
  [[ "$1" == "windows" ]] && exe="doorpass-helper.exe"
  mkdir -p "${out}"
  echo "==> doorpass-helper ${VERSION} for $1/$2"
  local envs=()
  [[ "$1" == "darwin" ]] && envs=("${MAC_ENV[@]}")
  (cd "${ROOT}" && env ${envs[@]+"${envs[@]}"} GOOS="$1" GOARCH="$2" CGO_ENABLED=1 CC="${4:-}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${out}/${exe}" ./desktop/helper 2>&1 | grep -v 'ld: warning' || true)
  [[ -f "${out}/${exe}" ]] || { echo "helper build failed for $1/$2" >&2; exit 1; }
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

build_mac() {
  build_helper darwin arm64 mac-arm64
  build_helper darwin amd64 mac-x64
  local out="${BUILD}/mac"
  rm -rf "${out}" && mkdir -p "${out}"
  for arch in arm64 amd64; do
    echo "==> DoorPass ${VERSION} for darwin/${arch}"
    (cd "${APP}" && env "${MAC_ENV[@]}" GOOS=darwin GOARCH="${arch}" CGO_ENABLED=1 \
      go build -trimpath -ldflags "${LDFLAGS}" -o "${out}/DoorPass-${arch}" . 2>&1 | grep -v 'ld: warning' || true)
    [[ -f "${out}/DoorPass-${arch}" ]] || { echo "build failed for ${arch}" >&2; exit 1; }
  done

  local bundle="${out}/DoorPass.app"
  mkdir -p "${bundle}/Contents/MacOS" "${bundle}/Contents/Resources/helper"
  lipo -create -output "${bundle}/Contents/MacOS/DoorPass" "${out}/DoorPass-arm64" "${out}/DoorPass-amd64"
  lipo -create -output "${bundle}/Contents/Resources/helper/doorpass-helper" \
    "${APP}/helper/mac-arm64/doorpass-helper" "${APP}/helper/mac-x64/doorpass-helper"
  sed "s/@VERSION@/${VERSION}/g" "${PKG}/Info.plist" > "${bundle}/Contents/Info.plist"

  local iconset="${out}/icon.iconset"
  mkdir -p "${iconset}"
  for s in 16 32 128 256 512; do
    sips -z "$s" "$s" "${APP}/assets/icon.png" --out "${iconset}/icon_${s}x${s}.png" >/dev/null
    sips -z $((s * 2)) $((s * 2)) "${APP}/assets/icon.png" --out "${iconset}/icon_${s}x${s}@2x.png" >/dev/null
  done
  iconutil -c icns "${iconset}" -o "${bundle}/Contents/Resources/icon.icns"

  # Ad-hoc signatures: required on Apple silicon; not a Developer ID.
  codesign --force -s - "${bundle}/Contents/Resources/helper/doorpass-helper"
  codesign --force -s - "${bundle}"

  local stage="${out}/dmg"
  mkdir -p "${stage}"
  cp -R "${bundle}" "${stage}/"
  ln -s /Applications "${stage}/Applications"
  hdiutil create -quiet -volname "DoorPass" -srcfolder "${stage}" -format ULMO -ov "${out}/DoorPass-${VERSION}-mac.dmg"
  mkdir -p "${RELEASE_DIR}"
  cp "${out}/DoorPass-${VERSION}-mac.dmg" "${RELEASE_DIR}/"
}

build_win() {
  build_helper windows amd64 win-x64 x86_64-w64-mingw32-gcc
  fetch_wintun
  local out="${BUILD}/win"
  rm -rf "${out}" && mkdir -p "${out}/app/helper"

  # Icon and Windows manifest (DPI awareness, common controls) for the exe.
  sips -z 256 256 "${APP}/assets/icon.png" --out "${out}/icon256.png" >/dev/null
  sips -s format ico "${out}/icon256.png" --out "${out}/icon.ico" >/dev/null
  (cd "${APP}" && go run "${GOWINRES}" simply --arch amd64 --icon "${out}/icon256.png" --manifest gui \
    --product-name DoorPass --product-version "${VERSION}" --file-version "${VERSION}" \
    --file-description DoorPass --copyright "Based on Intra by Jigsaw Operations LLC" --out rsrc)

  echo "==> DoorPass ${VERSION} for windows/amd64"
  (cd "${APP}" && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags "${LDFLAGS} -H windowsgui" -o "${out}/app/DoorPass.exe" .)
  rm -f "${APP}"/rsrc_windows_*.syso
  cp "${APP}/helper/win-x64/"* "${out}/app/helper/"

  cp "${PKG}/doorpass.nsi" "${out}/"
  local setup="DoorPass-${VERSION}-win-x64-setup.exe"
  if command -v makensis >/dev/null; then
    (cd "${out}" && makensis -V2 -DVERSION="${VERSION}" -DSRC=app -DICON=icon.ico -DOUTFILE="${setup}" doorpass.nsi)
  else
    echo "==> makensis via Docker (debian:trixie-slim nsis)"
    docker run --rm -e DEBIAN_FRONTEND=noninteractive -v "${out}:/w" -w /w debian:trixie-slim sh -c \
      "apt-get -qq update >/dev/null && apt-get -qq install -y nsis >/dev/null && makensis -V2 -DVERSION=${VERSION} -DSRC=app -DICON=icon.ico -DOUTFILE=${setup} doorpass.nsi"
  fi
  mkdir -p "${RELEASE_DIR}"
  cp "${out}/${setup}" "${RELEASE_DIR}/"
}

(cd "${ROOT}" && go vet ./desktop/helper && go test ./desktop/helper)
(cd "${APP}" && go vet .)

case "${TARGET}" in
  mac) build_mac ;;
  win) build_win ;;
  all) build_mac; build_win ;;
  *) echo "usage: $0 [mac|win|all]" >&2; exit 2 ;;
esac

(cd "${RELEASE_DIR}" && rm -f ._* && shasum -a 256 DoorPass-* > SHA256SUMS.txt && rm -f ._*)
echo "==> Installers in ${RELEASE_DIR}"
ls -la "${RELEASE_DIR}"
