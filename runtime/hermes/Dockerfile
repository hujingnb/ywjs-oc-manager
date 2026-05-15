# Hermes Agent runtime 镜像,对应 OpenClaw 时代的 runtime/openclaw/Dockerfile。
# manager 创建 app 时通过 docker run 启动本镜像,挂载 apps/<app_id>/.hermes 到 /opt/data。
# 容器启动即 ready,不允许运行时再装任何依赖。
#
# 注意: hermes install.sh 会去 hermes-agent.nousresearch.com / GitHub 拉源码与 Node tarball,
# 这部分走第三方 CDN, 无法整体国内化; 此处仅把基础镜像走 docker.1ms.run 加速 Docker Hub 拉取。

ARG DOCKER_HUB_MIRROR=docker.1ms.run/library
FROM ${DOCKER_HUB_MIRROR}/python:3.13-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    HERMES_HOME=/opt/data \
    PATH=/root/.local/bin:$PATH

# Debian apt 源替换为国内镜像, 加速 apt-get install 拉包;
# CI 走专网内 mirror 时可 --build-arg DEBIAN_MIRROR_HOST=<host> 覆盖。
# 同时处理新版 deb822 (sources.list.d/debian.sources, bookworm 默认) 和旧版 sources.list,
# 不存在的文件不报错; 若两者都不存在, 后续 apt-get update 自然会让构建失败暴露镜像格式变更。
ARG DEBIAN_MIRROR_HOST=mirrors.aliyun.com
RUN set -eux; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
      [ -f "$f" ] && sed -i \
        -e "s|deb.debian.org|${DEBIAN_MIRROR_HOST}|g" \
        -e "s|security.debian.org|${DEBIAN_MIRROR_HOST}|g" "$f" || true; \
    done

# 系统依赖:hermes install.sh 需要 curl + git,运行时需要 ripgrep + ffmpeg(hermes 工具默认要求),
# tini 用于在 PID 1 收割 hermes 派生的子进程(MCP stdio / git 等),
# xz-utils 提供 xz 二进制,install.sh 解压 node-v22 的 .tar.xz tarball 必需。
RUN apt-get update && apt-get install -y --no-install-recommends \
      curl ca-certificates git ripgrep ffmpeg tini xz-utils && \
    rm -rf /var/lib/apt/lists/*

# 上游 install.sh 装 hermes-agent 主体 + 依赖(uv 自动拉 Python 3.11+,装 venv,clone repo)。
# --skip-setup 跳过交互式向导;ref 由 version.txt 决定(后续可参数化)。
ARG HERMES_REF=main
RUN curl -fsSL https://hermes-agent.nousresearch.com/install.sh \
      | bash -s -- --skip-setup --branch ${HERMES_REF}

# Node.js 被 install.sh 装到 ${HERMES_HOME}/node = /opt/data/node。
# 但 manager 启动容器时会把 apps/<app_id>/.hermes bind mount 到 /opt/data,
# 整目录覆盖会让镜像内的 Node 失效。把 Node 重定位到 volume 外,并修正 PATH
# 上的 /root/.local/bin/{node,npm,npx} 软链(install.sh 在那建的入口)。
RUN mv /opt/data/node /opt/hermes-node && \
    ln -sfn /opt/hermes-node/bin/node /root/.local/bin/node && \
    ln -sfn /opt/hermes-node/bin/npm  /root/.local/bin/npm && \
    ln -sfn /opt/hermes-node/bin/npx  /root/.local/bin/npx

# 显式预装 weixin platform 必需的依赖,避免容器启动时走 lazy_deps.py(用户硬约束:
# 容器启动即 ready,运行时不装任何东西)。
# install.sh 在 root 用户下用 FHS layout,代码装到 /usr/local/lib/hermes-agent/。
# 注意:uv 创建的 venv 不带传统 pip 二进制(uv 自己取代 pip 角色),所以用 `uv pip` 装。
# `uv` 命令通过 install.sh 装到 /root/.local/bin/(已在 PATH 中)。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      aiohttp cryptography qrcode

# manager 项目自带的入口脚本。
COPY scripts/oc-weixin-login.py /usr/local/bin/oc-weixin-login
COPY scripts/healthcheck.sh /usr/local/bin/oc-healthcheck
RUN chmod +x /usr/local/bin/oc-weixin-login /usr/local/bin/oc-healthcheck

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/oc-healthcheck"]

VOLUME /opt/data

ENTRYPOINT ["/usr/bin/tini", "-g", "--", "hermes"]
CMD ["gateway", "run"]
