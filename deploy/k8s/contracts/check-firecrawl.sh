#!/usr/bin/env bash
# 校验共享 Firecrawl 清单的基础设施契约，防止临时状态或缺失 HPA 被误发布。
set -euo pipefail

manifest=${1:?用法: check-firecrawl.sh <firecrawl.yaml>}

kubectl apply --dry-run=client -f "$manifest" >/dev/null
test "$(rg -c '^kind: Deployment$' "$manifest")" -eq 9
test "$(rg -c '^kind: HorizontalPodAutoscaler$' "$manifest")" -eq 6
! rg -q '^kind: PersistentVolumeClaim$' "$manifest"
! rg -q '^kind: Ingress$' "$manifest"
! rg -q 'image: .*:latest' "$manifest"
rg -q 'name: firecrawl-api' "$manifest"
rg -q 'namespace: oc-firecrawl' "$manifest"
rg -q 'emptyDir: \{\}' "$manifest"
for target in firecrawl-api firecrawl-scrape-worker firecrawl-extract-worker firecrawl-nuq-worker firecrawl-nuq-prefetch-worker firecrawl-playwright; do
  rg -q "name: ${target}" "$manifest"
done
rg -q 'maxReplicas: 3' "$manifest"
test "$(rg -c 'maxReplicas: 5' "$manifest")" -eq 4
rg -q 'maxReplicas: 4' "$manifest"
rg -q 'averageUtilization: 70' "$manifest"
rg -q 'averageUtilization: 75' "$manifest"
# API 入口只接受两类 Hermes namespace；不应意外增加集群外入口。
rg -q 'kubernetes.io/metadata.name: oc-apps' "$manifest"
rg -q 'kubernetes.io/metadata.name: oc-aicc' "$manifest"
