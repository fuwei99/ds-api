#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT_DIR"

source "${ROOT_DIR}/scripts/release-targets.sh"

OUT_DIR="${ROOT_DIR}/.tmp/cross-build"

build_one() {
  local goos="$1" goarch="$2" goarm="$3" label="$4"
  local out
  out="${OUT_DIR}/${label}/ds2api"
  if [[ "$goos" == "windows" ]]; then
    out="${out}.exe"
  fi

  echo "[cross-build] ${label}"
  mkdir -p "$(dirname "$out")"
  if [[ "$goarm" == "-" ]]; then
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -buildvcs=false -trimpath -o "$out" ./cmd/ds2api
  else
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
      go build -buildvcs=false -trimpath -o "$out" ./cmd/ds2api
  fi
}

if [[ "${1:-}" == "--build-one" ]]; then
  shift
  build_one "$@"
  exit 0
fi

jobs="${CROSS_BUILD_JOBS:-}"
if [[ -z "$jobs" ]]; then
  if command -v nproc >/dev/null 2>&1; then
    jobs="$(nproc)"
  elif command -v sysctl >/dev/null 2>&1; then
    jobs="$(sysctl -n hw.ncpu)"
  else
    jobs="2"
  fi
fi

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

# CROSS_BUILD_TARGETS_FILTER 用空格或逗号分隔的包标签（如 "linux_amd64"），
# 限定本次只交叉编译其中一部分平台；留空则编译 release-targets.sh 中声明的全部平台。
filter="${CROSS_BUILD_TARGETS_FILTER:-}"
selected=()
for target in "${DS2API_RELEASE_TARGETS[@]}"; do
  read -r goos goarch goarm label <<< "$target"
  if [[ -z "$filter" ]]; then
    selected+=("$target")
    continue
  fi
  for want in ${filter//,/ }; do
    if [[ "$want" == "$label" ]]; then
      selected+=("$target")
      break
    fi
  done
done

if [[ "${#selected[@]}" -eq 0 ]]; then
  echo "[cross-build] CROSS_BUILD_TARGETS_FILTER='${filter}' matched no target." >&2
  echo "[cross-build] available labels: ${DS2API_RELEASE_TARGETS[*]##* }" >&2
  exit 1
fi

if [[ "$jobs" -le 1 ]]; then
  for target in "${selected[@]}"; do
    read -r goos goarch goarm label <<< "$target"
    build_one "$goos" "$goarch" "$goarm" "$label"
  done
else
  printf '%s\n' "${selected[@]}" \
    | xargs -L 1 -P "$jobs" bash "${ROOT_DIR}/tests/scripts/check-cross-build.sh" --build-one
fi
