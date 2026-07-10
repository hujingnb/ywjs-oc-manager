# manager-web 生产镜像:多阶段构建。
#   构建阶段: node:22-alpine 跑 `npm ci && npm run build` 产出 dist/
#   运行阶段: nginx:alpine 提供静态资源 + SPA 路由兜底
# 构建上下文为 web/(本目录),前端工程相对自包含,无需把整个仓库塞进上下文。
# deploy/manage/docker-compose.yml 通过 OCM_WEB_IMAGE 引用本镜像;
# 外层 manager-nginx 已完成 TLS 与 /api 反代,因此本镜像内的 nginx 只负责静态资源。

ARG NODE_VERSION=22
ARG NGINX_VERSION=1.27

# 公网基础镜像统一走移动云私有仓库的 public 命名空间, 该命名空间内
# 已预先镜像同步好下方使用的全部 Docker Hub 原始镜像。
# 切回公网原始仓库可 --build-arg DOCKER_HUB_MIRROR=docker.io/library。
ARG DOCKER_HUB_MIRROR=ywjs-26257ea5.ecis.huabei-3.cmecloud.cn/public

# ---- 构建阶段 -------------------------------------------------------------
# 原 docker.io/library/node:22-alpine → 现 ywjs-26257ea5.ecis.huabei-3.cmecloud.cn/public/node:22-alpine
FROM ${DOCKER_HUB_MIRROR}/node:${NODE_VERSION}-alpine AS builder

WORKDIR /src

# npm registry 默认走 npmmirror 国内源, CI 走专网内 mirror 可通过 --build-arg 覆盖。
# 使用 NPM_CONFIG_REGISTRY 而非 `npm config set`, 避免向用户目录写入 .npmrc 残留。
ARG NPM_REGISTRY=https://registry.npmmirror.com
ENV NPM_CONFIG_REGISTRY=${NPM_REGISTRY}
# onnxruntime-node 的 npm 包已内置 CPU 二进制; Docker 静态构建不需要 CUDA provider,
# 否则 postinstall 会默认从 GitHub Releases 拉取 GPU 包, 国内网络下容易构建失败。
ENV ONNXRUNTIME_NODE_INSTALL_CUDA=skip

# 先复制 manifest 并 npm ci 安装依赖,后续业务代码变更不会失效该层
COPY package.json package-lock.json ./

# npm ci 严格按 lockfile 安装,避免生产构建出现版本漂移
RUN npm ci --no-audit --no-fund

# 复制其余源码;.dockerignore 已剔除 node_modules、dist、tests/、test-results/
COPY . .

# vite build 默认产物输出到 dist/, openapi 类型 web/src/api/generated.ts 已入仓
RUN npm run build

# ---- 运行阶段 -------------------------------------------------------------
# 原 docker.io/library/nginx:1.27-alpine → 现 ywjs-26257ea5.ecis.huabei-3.cmecloud.cn/public/nginx:1.27-alpine
FROM ${DOCKER_HUB_MIRROR}/nginx:${NGINX_VERSION}-alpine

# 替换默认 server: 开启 HTML5 history 兜底、关闭目录索引、为 vite 产物配置缓存策略
COPY nginx.conf /etc/nginx/conf.d/default.conf

# vite build 产物拷贝到 nginx 默认 root
COPY --from=builder /src/dist /usr/share/nginx/html

# 80: 仅在 deploy/manage 的内部网络供外层 nginx 反代,不直接对外
EXPOSE 80
