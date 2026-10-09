#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT_DIR"

# CI 里不能覆盖 GOCACHE：actions/setup-go 缓存的是 `go env GOCACHE` 指向的默认目录，
# 覆盖成仓库内路径后，它恢复出来的缓存一次都不会被读到，每次都全量冷编译
# （windows-latest 上这一步约 2.5 分钟，而测试执行本身只占十几秒）。
# 本地仍默认用仓库内缓存，方便随 .tmp 一起丢弃。
if [ -z "${GOCACHE:-}" ] && [ -z "${CI:-}" ]; then
  export GOCACHE="${ROOT_DIR}/.tmp/go-build-cache"
fi
if [ -n "${GOCACHE:-}" ]; then
  mkdir -p "$GOCACHE"
fi

go test ./... "$@"
