#!/usr/bin/env bash
# build-bridge-packages.sh —— 打包内置 Mihomo 内核的发行包（一号一 IP 代理桥）。
#
# 产物（输出到 dist/）：
#   - ds2api_<VERSION>_windows_amd64.zip   含 mihomo.exe，解压即用（start.bat）
#   - ds2api_<VERSION>_linux_amd64.tar.gz  含 mihomo（+x），解压即用（start.sh）
#
# 可覆盖的环境变量：
#   BRIDGE_VERSION    发行版本号（默认 v3.5.0）
#   MIHOMO_VERSION    mihomo 内核版本（默认 v1.19.29）
#   MIHOMO_MIRROR     GitHub 下载加速前缀（默认 https://ghfast.top/，直连失败才兜底）
#   SKIP_DOWNLOAD     设为 1 时仅使用 .tmp/mihomo-dl 缓存，不联网
#   http_proxy / https_proxy  需要走本机代理下载时显式声明（curl 会自动使用）
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${BRIDGE_VERSION:-v3.5.0}"
MIHOMO_VERSION="${MIHOMO_VERSION:-v1.19.29}"
MIRROR="${MIHOMO_MIRROR:-https://ghfast.top/}"
CACHE_DIR=".tmp/mihomo-dl"

mkdir -p dist "${CACHE_DIR}"

release_url() {
    echo "https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/$1"
}

# ensure_cached <asset>：确保缓存存在并向 stdout 输出缓存路径（日志走 stderr）。
# 下载先走镜像，失败再直连 GitHub。
ensure_cached() {
    local asset="$1" url
    if [[ -f "${CACHE_DIR}/${asset}" ]]; then
        echo "[bridge-pkg] cache hit: ${asset}" >&2
        echo "${CACHE_DIR}/${asset}"
        return 0
    fi
    if [[ "${SKIP_DOWNLOAD:-0}" == "1" ]]; then
        echo "[bridge-pkg] SKIP_DOWNLOAD=1 but cache missing: ${asset}" >&2
        return 1
    fi
    for url in "${MIRROR}$(release_url "$asset")" "$(release_url "$asset")"; do
        echo "[bridge-pkg] downloading ${url}" >&2
        if curl -fL --retry 2 --connect-timeout 20 -o "${CACHE_DIR}/${asset}.part" "$url"; then
            mv "${CACHE_DIR}/${asset}.part" "${CACHE_DIR}/${asset}"
            echo "${CACHE_DIR}/${asset}"
            return 0
        fi
        rm -f "${CACHE_DIR}/${asset}.part"
    done
    echo "[bridge-pkg] download failed: ${asset}" >&2
    return 1
}

# make_zip <dir> <zipfile>：zip 不存在时用 PowerShell Compress-Archive 兜底（Windows）。
make_zip() {
    local dir="$1" out="$2"
    if command -v zip >/dev/null 2>&1; then
        (cd "$(dirname "$dir")" && zip -rq "$out" "$(basename "$dir")")
    else
        powershell.exe -NoProfile -Command \
            "Compress-Archive -Force -Path '$(cygpath -w "$dir")' -DestinationPath '$(cygpath -w "$out")'"
    fi
}

write_start_bat() {
    cat > "$1" <<'EOF'
@echo off
setlocal EnableDelayedExpansion
cd /d "%~dp0"
chcp 65001 >nul

title DS2API (Mihomo Bridge)

REM ---------------- defaults ----------------
if not defined PORT set PORT=5001
if not defined DS2API_ADMIN_KEY set DS2API_ADMIN_KEY=ds2api-local-test-key

REM ---------------- first-run config ----------------
if not exist config.json (
    copy /y config.example.json config.json >nul
    echo [init] created config.json from config.example.json
)

REM ---------------- mihomo binary check ----------------
if not exist mihomo.exe (
    echo [warn] mihomo.exe NOT found in this folder.
    echo        The proxy bridge cannot start until you download it from:
    echo        https://github.com/MetaCubeX/mihomo/releases
    echo        Pick mihomo-windows-amd64-*.zip, extract it, rename the exe
    echo        to mihomo.exe and place it next to ds2api.exe.
    echo        ds2api itself can still run without it.
    echo.
    goto :start
)

mihomo.exe -v >nul 2>&1
if errorlevel 1 (
    echo [warn] mihomo.exe exists but FAILED to run ^(wrong architecture?^).
    echo        Please download the WINDOWS amd64 build from:
    echo        https://github.com/MetaCubeX/mihomo/releases
    echo.
) else (
    echo [ok] mihomo.exe is runnable.
)

:start
echo ============================================
echo  DS2API starting...
echo    URL       : http://127.0.0.1:%PORT%
echo    Admin UI  : http://127.0.0.1:%PORT%/admin
echo    Admin key : %DS2API_ADMIN_KEY%
echo ============================================
echo  Open the Admin UI, go to "Proxy Bridge" tab,
echo  enable the bridge, add a subscription, bind accounts.
echo  Press Ctrl+C in this window to stop.
echo.

ds2api.exe

echo.
echo ds2api exited with code %ERRORLEVEL%.
pause
endlocal
EOF
}

write_start_sh() {
    cat > "$1" <<'EOF'
#!/usr/bin/env sh
# DS2API (Mihomo Bridge) one-shot launcher for Linux.
set -e
cd "$(dirname "$0")"

PORT="${PORT:-5001}"
export PORT
: "${DS2API_ADMIN_KEY:=ds2api-local-test-key}"
export DS2API_ADMIN_KEY

if [ ! -f config.json ]; then
    cp config.example.json config.json
    echo "[init] created config.json from config.example.json"
fi

if [ ! -x ./mihomo ]; then
    echo "[warn] ./mihomo not found or not executable; proxy bridge will not work."
else
    echo "[ok] mihomo: $(./mihomo -v 2>/dev/null | head -n1)"
fi

echo "============================================"
echo " DS2API starting..."
echo "   URL       : http://127.0.0.1:${PORT}"
echo "   Admin UI  : http://127.0.0.1:${PORT}/admin"
echo "   Admin key : ${DS2API_ADMIN_KEY}"
echo "============================================"

exec ./ds2api
EOF
    chmod +x "$1"
}

build_windows() {
    local pkg="ds2api_${VERSION}_windows_amd64"
    local stage="dist/${pkg}"
    echo "[bridge-pkg] building ${pkg}"
    rm -rf "$stage"
    mkdir -p "${stage}/static" "${stage}/docs"

    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -buildvcs=false -trimpath \
        -ldflags="-s -w -X ds2api/internal/version.BuildVersion=${VERSION}" \
        -o "${stage}/ds2api.exe" ./cmd/ds2api

    cp -R static/admin "${stage}/static/admin"
    cp config.example.json "${stage}/"
    cp docs/MIHOMO_BRIDGE.md "${stage}/docs/"
    write_start_bat "${stage}/start.bat"

    local win_zip
    win_zip="$(ensure_cached "mihomo-windows-amd64-${MIHOMO_VERSION}.zip")"
    local tmp="${CACHE_DIR}/win-extract"
    rm -rf "$tmp"
    mkdir -p "$tmp"
    unzip -q "$win_zip" -d "$tmp"
    local bin
    bin="$(find "$tmp" -name 'mihomo*.exe' | head -n1)"
    test -n "$bin"
    cp "$bin" "${stage}/mihomo.exe"

    rm -f "dist/${pkg}.zip"
    make_zip "$stage" "dist/${pkg}.zip"
    rm -rf "$stage"
    echo "[bridge-pkg] done: dist/${pkg}.zip"
}

build_linux() {
    local pkg="ds2api_${VERSION}_linux_amd64"
    local stage="dist/${pkg}"
    echo "[bridge-pkg] building ${pkg}"
    rm -rf "$stage"
    mkdir -p "${stage}/static" "${stage}/docs"

    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath \
        -ldflags="-s -w -X ds2api/internal/version.BuildVersion=${VERSION}" \
        -o "${stage}/ds2api" ./cmd/ds2api

    cp -R static/admin "${stage}/static/admin"
    cp config.example.json "${stage}/"
    cp docs/MIHOMO_BRIDGE.md "${stage}/docs/"
    write_start_sh "${stage}/start.sh"

    local linux_gz
    linux_gz="$(ensure_cached "mihomo-linux-amd64-${MIHOMO_VERSION}.gz")"
    gunzip -c "$linux_gz" > "${stage}/mihomo"
    chmod +x "${stage}/ds2api" "${stage}/mihomo"

    # Windows 下 bsdtar 无法可靠保留可执行位，用 Go 打包器显式写入权限。
    rm -f "dist/${pkg}.tar.gz"
    go run ./scripts/targzpack "dist/${pkg}.tar.gz" "${pkg}" "$stage"
    rm -rf "$stage"
    echo "[bridge-pkg] done: dist/${pkg}.tar.gz"
}

target="${1:-all}"
case "$target" in
    windows) build_windows ;;
    linux) build_linux ;;
    all) build_windows; build_linux ;;
    *)
        echo "usage: $0 [all|windows|linux]" >&2
        exit 1
        ;;
esac
