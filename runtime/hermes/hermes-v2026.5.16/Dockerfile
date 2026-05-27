# runtime/hermes/hermes-v2026.5.16/Dockerfile
# hermes-v2026.5.16 variant 镜像构建脚本。build context = 本目录，COPY 不跨目录。
# 输入：HERMES_REF (上游 git ref，从 version.txt 读)；OC_IMAGE_VARIANT (variant 标识)；OC_BUILD_TS (构建时间戳)。

ARG DOCKER_HUB_MIRROR=crpi-nu3ibz4f07feyghi.cn-beijing.personal.cr.aliyuncs.com/ywjs_public
FROM ${DOCKER_HUB_MIRROR}/python:3.13-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    HERMES_HOME=/opt/data \
    PATH=/root/.local/bin:$PATH

# Debian apt 源替换为国内镜像加速 apt-get install。
ARG DEBIAN_MIRROR_HOST=mirrors.aliyun.com
RUN set -eux; for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
      [ -f "$f" ] && sed -i \
        -e "s|deb.debian.org|${DEBIAN_MIRROR_HOST}|g" \
        -e "s|security.debian.org|${DEBIAN_MIRROR_HOST}|g" "$f" || true; \
    done

# 系统依赖：hermes install.sh 需要 curl + git；运行时需要 ripgrep + ffmpeg；tini 用作 PID 1；xz-utils 用来解压 node tarball。
RUN apt-get update && apt-get install -y --no-install-recommends \
      curl ca-certificates git ripgrep ffmpeg tini xz-utils && \
    rm -rf /var/lib/apt/lists/*

# hermes 上游安装：install.sh 装 hermes-agent + uv + Node + skill 库。
# install.sh 的 node 下载、git clone 等失败分支全部走 log_warn 后 exit 0，
# 会把"装了一半"甚至"一行没装"的半成品当作成功结果留在镜像层；docker
# 看不见层内容是否真的健全，会把这种空/坏层缓存为"成功"，后续构建无论
# 远程脚本如何修复都会无限复用这个坏层（已出现过该缓存命中后下游
# `mv /opt/data/node` 一直报 cannot stat 的事故，单纯重跑 make 也绕不开）。
# 这里把关键产物校验并进同一条 RUN：缺 hermes-agent / venv python /
# Node 任一项即 exit 1，让 docker build 不缓存坏层；同时命令字符串变更
# 也顺带让历史上已存在的空缓存层失效，本次构建必然重新跑 install.sh。
ARG HERMES_REF
RUN set -e; \
    curl -fsSL https://hermes-agent.nousresearch.com/install.sh \
      | bash -s -- --skip-setup --branch ${HERMES_REF}; \
    for p in \
        /usr/local/lib/hermes-agent \
        /usr/local/lib/hermes-agent/venv/bin/python \
        /opt/data/node/bin/node; \
    do \
        [ -e "$p" ] || { echo "[install.sh guard] 缺少关键产物 $p——install.sh 可能静默失败（如 nodejs.org / github.com 不通），中断构建以阻止坏层进入缓存" >&2; exit 1; }; \
    done

# Node.js 默认装到 /opt/data/node，与运行时挂载点重叠；移到 /opt/hermes-node 并修软链。
RUN mv /opt/data/node /opt/hermes-node && \
    ln -sfn /opt/hermes-node/bin/node /root/.local/bin/node && \
    ln -sfn /opt/hermes-node/bin/npm  /root/.local/bin/npm && \
    ln -sfn /opt/hermes-node/bin/npx  /root/.local/bin/npx

# 显式预装 weixin platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      aiohttp cryptography qrcode

# oc-entrypoint Python 依赖：pyyaml 用于 manifest 解析；pytest 用于镜像构建期自检。
RUN uv pip install --system --no-cache-dir pyyaml pytest jsonschema

# 本目录内 COPY（build context = 本 variant 目录）
COPY oc-entrypoint.py     /usr/local/bin/oc-entrypoint
COPY oc-doctor.py         /usr/local/bin/oc-doctor
COPY oc-info.py           /usr/local/bin/oc-info
COPY oc-channel-login.py  /usr/local/bin/oc-channel-login
COPY oc-channel-status.py /usr/local/bin/oc-channel-status
COPY oc-channel-unbind.py /usr/local/bin/oc-channel-unbind
COPY oc-kanban.py         /usr/local/bin/oc-kanban
COPY kanban-contract/     /usr/local/lib/oc-kanban/contract/
COPY oc-cron.py           /usr/local/bin/oc-cron
COPY cron-contract/       /usr/local/lib/oc-cron/contract/
COPY oc-kb.py             /usr/local/bin/oc-kb
COPY healthcheck.sh       /usr/local/bin/oc-healthcheck
COPY lib/                 /usr/local/lib/oc-entrypoint/lib/
COPY renderer/            /usr/local/lib/oc-entrypoint/renderer/
COPY migrator/            /usr/local/lib/oc-entrypoint/migrator/
COPY tests/               /usr/local/lib/oc-entrypoint/tests/

RUN chmod +x /usr/local/bin/oc-entrypoint /usr/local/bin/oc-doctor \
            /usr/local/bin/oc-info /usr/local/bin/oc-channel-login \
            /usr/local/bin/oc-channel-status /usr/local/bin/oc-channel-unbind \
            /usr/local/bin/oc-kanban /usr/local/bin/oc-cron /usr/local/bin/oc-kb \
            /usr/local/bin/oc-healthcheck

# 镜像身份信息写到固定位置，供 oc-info 读取
ARG OC_IMAGE_VARIANT
ARG OC_BUILD_TS
RUN printf '{"variant":"%s","hermes_upstream_ref":"%s","built_at":"%s"}\n' \
      "$OC_IMAGE_VARIANT" "$HERMES_REF" "$OC_BUILD_TS" \
      > /etc/oc-image.json

ENV OC_IMAGE_VARIANT=${OC_IMAGE_VARIANT}

# 镜像构建期即执行 runtime 自检；任一单元测试失败都会让 docker build 失败。
RUN PYTHONDONTWRITEBYTECODE=1 python -m pytest /usr/local/lib/oc-entrypoint/tests/ -v -p no:cacheprovider

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/oc-healthcheck"]

VOLUME /opt/data
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/oc-entrypoint"]
CMD []
