# runtime/ops/Dockerfile
# app pod 运行时侧 ops 镜像：承载 initContainer(oc-restore) 与 sidecar(oc-sync/oc-presync)。
# 仅装标准 S3 客户端与搬运工具，与 hermes 主镜像解耦（agent 与运维搬运职责分离）。
ARG ALPINE_MIRROR=docker.io/library
FROM ${ALPINE_MIRROR}/alpine:3.20

# aws-cli：标准 S3 客户端（vendor-neutral，对接任意 S3 兼容端点）。
# sqlite：state.db .backup 一致性快照。jq：解析 bootstrap JSON。curl：调 bootstrap / 下载 skills。
# bash：脚本解释器。coreutils：GNU date（-d RFC3339 解析过期时间）。tar：兼容性。ca-certificates：HTTPS。
RUN apk add --no-cache aws-cli sqlite jq curl bash coreutils tar ca-certificates

COPY bin/oc-lib.sh bin/oc-bootstrap bin/oc-restore bin/oc-sync bin/oc-presync /usr/local/bin/
RUN chmod +x /usr/local/bin/oc-bootstrap /usr/local/bin/oc-restore /usr/local/bin/oc-sync /usr/local/bin/oc-presync

# 默认无 CMD：pod spec 按容器角色覆盖 command 为 oc-restore / oc-sync。
