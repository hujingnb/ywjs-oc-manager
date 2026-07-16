#!/usr/bin/env bash
# 校验 local-up 会把 CoreDNS 的默认上游替换为可达公共 DNS，避免网页提取只能解析个别预设站点。
set -euo pipefail

makefile=${1:?用法: check-coredns-custom.sh <Makefile>}

rg -Fq 'forward \\. /etc/resolv\\.conf' "$makefile"
rg -Fq 'forward . 223.5.5.5 223.6.6.6' "$makefile"
rg -q 'rollout restart deploy/coredns' "$makefile"
