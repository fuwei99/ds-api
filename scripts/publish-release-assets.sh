#!/usr/bin/env bash
set -euo pipefail

# 把构建产物发布到指定 tag 的 Release；Release 不存在时创建它。
# 需要 GH_TOKEN 环境变量（GitHub Actions 里用 secrets.GITHUB_TOKEN）。
# 用法: bash scripts/publish-release-assets.sh <tag> <file> [file...]

tag="${1:-}"
if [[ -z "$tag" ]]; then
  echo "usage: $0 <tag> <file> [file...]" >&2
  exit 1
fi
shift

files=()
for f in "$@"; do
  if [[ -f "$f" ]]; then
    files+=("$f")
  else
    echo "[publish-release-assets] skip missing file: $f" >&2
  fi
done

if [[ "${#files[@]}" -eq 0 ]]; then
  echo "[publish-release-assets] no files to publish for ${tag}" >&2
  exit 1
fi

if gh release view "$tag" >/dev/null 2>&1; then
  echo "[publish-release-assets] uploading ${#files[@]} file(s) to existing release ${tag}"
  gh release upload "$tag" "${files[@]}" --clobber
else
  echo "[publish-release-assets] creating release ${tag} with ${#files[@]} file(s)"
  gh release create "$tag" "${files[@]}" --title "$tag" --notes ""
fi
