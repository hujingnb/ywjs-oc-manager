# manager-api 生产镜像：多阶段构建，最终镜像仅包含 alpine 基础包与三个静态 Go 二进制。
# 构建上下文必须为仓库根目录(需要访问 go.mod / go.sum / internal/ / cmd/ / runtime/)。
# deploy/manage/docker-compose.yml 通过 OCM_MANAGER_IMAGE 引用本镜像。

ARG GO_VERSION=1.25
ARG ALPINE_VERSION=3.20

# 公网基础镜像统一走 docker.1ms.run 的 Docker Hub 镜像加速,
# 国内构建机直连 docker.io 经常超时,且此处镜像可信。
ARG DOCKER_HUB_MIRROR=docker.1ms.run/library

# ---- 构建阶段 -------------------------------------------------------------
FROM ${DOCKER_HUB_MIRROR}/golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

# git: 部分 Go 模块依赖通过 git 协议拉取
# ca-certificates: GOPROXY 走 HTTPS 时校验证书链
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# 默认 GOPROXY 用国内源(goproxy.cn),CI 若走专网内 proxy 可通过 --build-arg 覆盖。
# GOSUMDB=off: goproxy.cn 不代理原始 sum.golang.org, 关闭后避免每次构建卡 sumdb 校验;
#              这是与 docker-compose.yml dev 容器一致的取舍, 模块校验仍由 go.sum 守住。
# CGO_ENABLED=0: 保证产物完全静态, 可直接在 alpine 上运行而无需 libc 兼容层。
ARG GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY} \
    GOSUMDB=off

# 先单独下载依赖, 使代码变更不致让模块缓存层失效
COPY go.mod go.sum ./
RUN go mod download

# 复制全部源码; 通过 .dockerignore 已剔除 web/、docs/、tmp/ 等无关目录
COPY . .

# 产出三个二进制:
#   oc-manager  : HTTP API 主进程, 容器默认 CMD
#   migrate     : 数据库迁移 CLI, 首次部署执行
#   seed-admin  : 平台管理员初始化, 首次部署执行
# 三者打入同一镜像, 便于运维只用一个镜像完成启动 / 迁移 / 应急种子操作
RUN go build -trimpath -ldflags='-s -w' -o /out/oc-manager ./cmd/server \
 && go build -trimpath -ldflags='-s -w' -o /out/migrate    ./cmd/migrate \
 && go build -trimpath -ldflags='-s -w' -o /out/seed-admin ./cmd/seed-admin

# ---- 运行阶段 -------------------------------------------------------------
FROM ${DOCKER_HUB_MIRROR}/alpine:${ALPINE_VERSION}

# ca-certificates: manager-api 调用 new-api / agent HTTPS 端点必需
# tzdata        : 让 TZ=Asia/Shanghai 在容器内生效
# wget          : deploy/manage/docker-compose.yml healthcheck 使用 `wget -qO- /healthz`
RUN apk add --no-cache ca-certificates tzdata wget && \
    mkdir -p /var/lib/oc-manager/data /var/lib/oc-manager/knowledge /etc/manager

COPY --from=builder /out/oc-manager /usr/local/bin/oc-manager
COPY --from=builder /out/migrate    /usr/local/bin/migrate
COPY --from=builder /out/seed-admin /usr/local/bin/seed-admin

# OCM_CONFIG: cmd/server 与 cmd/migrate 都通过该环境变量定位主配置
# deploy/manage/docker-compose.yml 将 config/manager.yaml 挂载到 /etc/manager/config.yaml
ENV OCM_CONFIG=/etc/manager/config.yaml \
    TZ=Asia/Shanghai

# manager-api 监听 8080(内部网络), 由外层 nginx 反代; 不需要直接对外
EXPOSE 8080

# 注意: 不切换非 root 用户。
# 本服务通过挂载 /var/run/docker.sock 调用宿主 Docker(imagesync 链路),
# docker socket 已是 root 等价权限, 容器内再切换非 root 既无安全增益,
# 还会让 socket 访问 / 数据目录 chown 变复杂。
WORKDIR /var/lib/oc-manager

# 用 CMD 而非 ENTRYPOINT,因为本镜像同时承载三个可调度入口:
#   - 默认启动: `oc-manager` 主进程(deploy/manage/docker-compose.yml 跑 up -d 时)
#   - 数据库迁移: `docker compose run --rm manager-api migrate up`
#   - 初始管理员: `docker compose run --rm manager-api seed-admin <user> <pass>`
# 三个二进制都在 PATH(/usr/local/bin),`docker compose run` 通过覆盖 CMD 直接调用即可,
# 无需用 --entrypoint 重写入口,也避免 ENTRYPOINT 把所有参数拼到 oc-manager 后面。
CMD ["oc-manager"]
