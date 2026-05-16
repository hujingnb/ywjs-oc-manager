# oc-runtime-agent 生产镜像:多阶段构建,从源码出发,最终镜像仅包含静态二进制与必要系统证书。
# 构建上下文必须为仓库根目录(需要访问 go.mod / internal/ / runtime/)。
# deploy/runtime-agent/docker-compose.yml 通过 OC_RUNTIME_AGENT_IMAGE 引用本镜像。

ARG GO_VERSION=1.25
# Go 1.25 的官方 alpine 变体只发布到 3.21/3.22/3.23, 没有 3.20;
# 取 3.22 作为 builder 与 final 共用的 alpine 主版本, 稳定且 docker.io 上有对应镜像。
ARG ALPINE_VERSION=3.22

# 公网基础镜像统一走 aliyun 私有 ACR 的 ywjs_public 命名空间, 该命名空间内
# 已预先镜像同步好下方使用的全部 Docker Hub 原始镜像。
# 切回公网原始仓库可 --build-arg DOCKER_HUB_MIRROR=docker.io/library。
ARG DOCKER_HUB_MIRROR=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public

# Alpine apk 仓库镜像主机: 国内构建机直连 dl-cdn.alpinelinux.org 经常握手超时,
# 默认替换为阿里云镜像(与上方 ACR 同地域同 BGP 出口, 拉取最稳定);
# 切回官方源可 --build-arg ALPINE_MIRROR=dl-cdn.alpinelinux.org,
# 改用清华 / 中科大可传 mirrors.tuna.tsinghua.edu.cn / mirrors.ustc.edu.cn。
ARG ALPINE_MIRROR=mirrors.aliyun.com

# ---- 构建阶段 -------------------------------------------------------------
# 原 docker.io/library/golang:1.25-alpine3.22 → 现 crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/golang:1.25-alpine3.22
FROM ${DOCKER_HUB_MIRROR}/golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# 全局 ARG 在 FROM 之后默认不可见, 需要在 stage 内 redeclare 才能被下方 RUN 引用。
ARG ALPINE_MIRROR

# git: 部分 Go 模块依赖走 git 协议拉取
# ca-certificates: GOPROXY HTTPS 校验
# 先把 /etc/apk/repositories 内的 dl-cdn.alpinelinux.org 整体替换为 ${ALPINE_MIRROR},
# 同时覆盖 main / community 两个仓库; 这样后续 apk add 都走国内镜像。
RUN sed -i "s|dl-cdn.alpinelinux.org|${ALPINE_MIRROR}|g" /etc/apk/repositories \
 && apk add --no-cache git ca-certificates

WORKDIR /src

# 默认 GOPROXY 用国内源(goproxy.cn),CI 走专网内 proxy 可通过 --build-arg 覆盖。
# GOSUMDB=off: goproxy.cn 不代理原始 sum.golang.org, 关闭以免构建卡 sumdb 校验;
#              与 docker-compose.yml dev 容器一致, 模块校验仍由 go.sum 守住。
# CGO_ENABLED=0: 产物完全静态, 直接在 alpine 上运行。
ARG GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY} \
    GOSUMDB=off

# 模块下载层独立, 业务源码改动不会让该层失效
COPY go.mod go.sum ./
RUN go mod download

# 复制源码;.dockerignore 已剔除 web/、docs/、tmp/ 等无关目录
COPY . .

# 唯一产物:oc-runtime-agent;同一二进制承担运行主进程与 `healthcheck` 子命令两种用途
# -X 将版本号注入 heartbeat.go 中的 agentVersion 变量,生产镜像上报 "master" 而非 "dev"。
RUN go build -trimpath \
    -ldflags="-s -w -X main.agentVersion=master" \
    -o /out/oc-runtime-agent ./runtime/agent

# ---- 运行阶段 -------------------------------------------------------------
# 原 docker.io/library/alpine:3.22 → 现 crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public/alpine:3.22
FROM ${DOCKER_HUB_MIRROR}/alpine:${ALPINE_VERSION}

# 全局 ARG 在 FROM 之后默认不可见, 需要在 stage 内 redeclare 才能被下方 RUN 引用。
ARG ALPINE_MIRROR

# ca-certificates: agent 调用 manager `/api/v1/agent/enroll` 与心跳走 HTTPS 必需
# tzdata        : 让 TZ=Asia/Shanghai 生效, 日志时间与运营期望一致
# 与 builder 阶段一致, 先把 apk 仓库切到国内镜像再安装。
RUN sed -i "s|dl-cdn.alpinelinux.org|${ALPINE_MIRROR}|g" /etc/apk/repositories && \
    apk add --no-cache ca-certificates tzdata && \
    mkdir -p /var/lib/oc-agent /etc/oc-agent

COPY --from=builder /out/oc-runtime-agent /usr/local/bin/oc-runtime-agent

# OC_AGENT_CONFIG: runtime/agent/main.go 优先读取该环境变量定位 agent.yaml
# deploy/runtime-agent/docker-compose.yml 将 config/agent.yaml 挂载到 /etc/oc-agent/agent.yaml
ENV OC_AGENT_CONFIG=/etc/oc-agent/agent.yaml \
    TZ=Asia/Shanghai

# 7001: Docker TLS proxy(gRPC); 7002: 文件 / scope HTTP API。两个端口仅对 manager 出口网段开放
EXPOSE 7001 7002

# 注意: 不切换非 root 用户。
# 本服务挂载宿主 /var/run/docker.sock 直接管理本机容器, docker socket 已是 root 等价权限,
# 切换非 root 既无安全增益, 又会让 socket 访问与数据目录权限对齐变复杂。
WORKDIR /var/lib/oc-agent

ENTRYPOINT ["/usr/local/bin/oc-runtime-agent"]
