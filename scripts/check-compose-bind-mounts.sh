#!/usr/bin/env bash
set -euo pipefail

compose_file="${1:-docker-compose.yml}"

if grep -Eq '^volumes:[[:space:]]*$' "$compose_file"; then
  echo "禁止使用顶层 Docker named volumes，请改用本地目录 bind mount。" >&2
  exit 1
fi

bad_mounts="$(
  awk '
    /^[[:space:]]{4}volumes:[[:space:]]*$/ {
      in_volumes=1
      next
    }
    in_volumes && /^[[:space:]]{4}[A-Za-z0-9_-]+:/ {
      in_volumes=0
    }
    in_volumes && /^[[:space:]]{6}-[[:space:]]+[^#]+:[^#]+/ {
      line=$0
      sub(/^[[:space:]]*-[[:space:]]+/, "", line)
      gsub(/^"|"$/, "", line)
      split(line, parts, ":")
      source=parts[1]
      gsub(/^"|"$/, "", source)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", source)
      if (source !~ /^\.\/.*/ && source !~ /^\// && source !~ /^\$\{PWD\}/ && source !~ /^\$\{HOME\}/) {
        print $0
      }
    }
  ' "$compose_file"
)"

if [[ -n "$bad_mounts" ]]; then
  echo "发现非本地 bind mount 的挂载项：" >&2
  echo "$bad_mounts" >&2
  exit 1
fi

echo "compose 挂载检查通过：未发现 named volume，service 挂载均为本地 bind mount。"
