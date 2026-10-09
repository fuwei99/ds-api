#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

source "${ROOT_DIR}/scripts/release-targets.sh"

build_one() {
  local tag="$1" build_version="$2" goos="$3" goarch="$4" goarm="$5" label="$6"
  local pkg stage bin

  pkg="ds2api_${tag}_${label}"
  stage="dist/${pkg}"
  bin="ds2api"
  if [[ "$goos" == "windows" ]]; then
    bin="ds2api.exe"
  fi

  echo "[release-archives] building ${label}"
  rm -rf "$stage"
  mkdir -p "${stage}/static"

  if [[ "$goarm" == "-" ]]; then
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build -buildvcs=false -trimpath -ldflags="-s -w -X ds2api/internal/version.BuildVersion=${build_version}" -o "${stage}/${bin}" ./cmd/ds2api
  else
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
      go build -buildvcs=false -trimpath -ldflags="-s -w -X ds2api/internal/version.BuildVersion=${build_version}" -o "${stage}/${bin}" ./cmd/ds2api
  fi

  for f in config.example.json .env.example LICENSE README.MD; do
    [ -f "$f" ] && cp "$f" "${stage}/"
  done
  cp -R static/admin "${stage}/static/admin"

  if [[ "$goos" == "windows" ]]; then
    (cd dist && zip -rq "${pkg}.zip" "${pkg}")
  else
    tar -C dist -czf "dist/${pkg}.tar.gz" "${pkg}"
  fi

  rm -rf "$stage"
}

if [[ "${1:-}" == "--build-one" ]]; then
  shift
  build_one "$@"
  exit 0
fi

tag="${RELEASE_TAG:-}"
if [[ -z "$tag" && -f VERSION ]]; then
  tag="$(tr -d '[:space:]' < VERSION)"
fi
if [[ -z "$tag" ]]; then
  echo "release tag is empty; set RELEASE_TAG or provide VERSION." >&2
  exit 1
fi

build_version="${BUILD_VERSION:-$tag}"
jobs="${RELEASE_BUILD_JOBS:-}"
if [[ -z "$jobs" ]]; then
  if command -v nproc >/dev/null 2>&1; then
    jobs="$(nproc)"
  elif command -v sysctl >/dev/null 2>&1; then
    jobs="$(sysctl -n hw.ncpu)"
  else
    jobs="2"
  fi
fi

mkdir -p dist

# RELEASE_TARGETS_FILTER 用空格或逗号分隔的包标签（如 "linux_amd64 windows_amd64"），
# 限定本次只构建其中一部分平台；留空则构建 release-targets.sh 中声明的全部平台。
filter="${RELEASE_TARGETS_FILTER:-}"
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
  echo "[release-archives] RELEASE_TARGETS_FILTER='${filter}' matched no target." >&2
  echo "[release-archives] available labels: ${DS2API_RELEASE_TARGETS[*]##* }" >&2
  exit 1
fi

if [[ "$jobs" -le 1 ]]; then
  for target in "${selected[@]}"; do
    read -r goos goarch goarm label <<< "$target"
    build_one "$tag" "$build_version" "$goos" "$goarch" "$goarm" "$label"
  done
else
  printf '%s\n' "${selected[@]}" \
    | xargs -L 1 -P "$jobs" bash "${ROOT_DIR}/scripts/build-release-archives.sh" --build-one "$tag" "$build_version"
fi
