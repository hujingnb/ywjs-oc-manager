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
# NuQ 直启 worker 必须使用 pg 后端，并从 Secret 获得完整连接串，不能依赖 harness 注入。
rg -q '^  NUQ_BACKEND: pg$' "$manifest"
rg -q '^  NUQ_DATABASE_URL:' "$manifest"
rg -q '^  NUQ_DATABASE_URL_LISTEN:' "$manifest"
test "$(rg -c 'secretRef: \{ name: firecrawl-runtime \}' "$manifest")" -eq 5
# ConfigMap 只能承载非敏感运行参数，连接串必须停留在前置 Secret 文档。
! awk '/^kind: ConfigMap$/{in_config_map=1; next} /^---$/{in_config_map=0} in_config_map && /NUQ_DATABASE_URL/{found=1} END{exit found ? 0 : 1}' "$manifest"
