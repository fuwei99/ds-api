#!/usr/bin/env bash
set -euo pipefail

# 等待指定 release 上出现期望的资产。
#
# 背景：gh release upload 返回成功，不代表 release 查询接口立刻能看到该资产，
# 上传与列表之间存在可见性延迟，下游 job 直接 gh release download 会拿到空列表
# 并报 "no assets to download"。此脚本轮询到全部资产可见后再返回。
#
# 用法: bash scripts/wait-release-assets.sh <tag> <timeout-seconds> <asset-name> [asset-name...]
# 需要 GH_TOKEN 环境变量。

tag="${1:-}"
timeout_seconds="${2:-300}"

if [[ -z "$tag" ]]; then
  echo "usage: $0 <tag> <timeout-seconds> <asset-name> [asset-name...]" >&2
  exit 1
fi
shift 2

if [[ "$#" -eq 0 ]]; then
  echo "[wait-release-assets] no asset name provided for ${tag}" >&2
  exit 1
fi

required=("$@")
deadline=$(( $(date +%s) + timeout_seconds ))

while :; do
  assets="$(gh release view "$tag" --json assets --jq '.assets[].name' 2>/dev/null || true)"

  missing=()
  for want in "${required[@]}"; do
    found=0
    while IFS= read -r name; do
      [[ -n "$name" ]] || continue
      if [[ "$name" == "$want" ]]; then
        found=1
        break
      fi
    done <<< "$assets"
    if [[ "$found" -ne 1 ]]; then
      missing+=("$want")
    fi
  done

  if [[ "${#missing[@]}" -eq 0 ]]; then
    echo "[wait-release-assets] all ${#required[@]} asset(s) visible on ${tag}"
    exit 0
  fi

  if [[ "$(date +%s)" -ge "$deadline" ]]; then
    echo "[wait-release-assets] timed out after ${timeout_seconds}s waiting for assets on ${tag}" >&2
    printf '  missing: %s\n' "${missing[@]}" >&2
    if [[ -n "$assets" ]]; then
      echo "  visible now:" >&2
      printf '    %s\n' "$assets" >&2
    else
      echo "  visible now: (none; release lookup returned no asset)" >&2
    fi
    exit 1
  fi

  echo "[wait-release-assets] waiting for ${#missing[@]} asset(s) on ${tag}: ${missing[*]:-}"
  sleep 15
done
