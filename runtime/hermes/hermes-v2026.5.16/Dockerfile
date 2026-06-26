# runtime/hermes/hermes-v2026.5.16/Dockerfile
# hermes-v2026.5.16 variant 镜像构建脚本。build context = 本目录，COPY 不跨目录。
# 输入：HERMES_REF (上游 git ref，从 version.txt 读)；OC_IMAGE_VARIANT (variant 标识)；OC_BUILD_TS (构建时间戳)。

ARG DOCKER_HUB_MIRROR=ywjs-26257ea5.ecis.huabei-3.cmecloud.cn/public
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

# 上游 install.sh 在 HERMES_HOME 设置时把工具装进 $HERMES_HOME/bin（=/opt/data/bin）、
# 把 node 装进 /opt/data/node，把 hermes 链到 /usr/local/bin，且不再创建 PATH 上的
# /root/.local/bin。/opt/data 是运行时挂载卷，卷内的 uv / node 会在容器启动时被覆盖，
# 因此必须在构建期把这些工具搬出 /opt/data 到镜像层固定路径并保证在 PATH 上：
# - node 仍移到 /opt/hermes-node，软链进 /root/.local/bin（先 mkdir 兜底落点，
#   install.sh 是 live 拉取、不锁版本，该目录是否预先存在不可依赖）；
# - uv / uvx 是独立可执行文件，直接搬到 /usr/local/bin（已在 PATH，hermes 也链在此）。
# 否则后续 `uv pip install` 会 uv: not found，运行时 node 也会随卷一起消失。
RUN mkdir -p /root/.local/bin && \
    mv /opt/data/node /opt/hermes-node && \
    ln -sfn /opt/hermes-node/bin/node /root/.local/bin/node && \
    ln -sfn /opt/hermes-node/bin/npm  /root/.local/bin/npm && \
    ln -sfn /opt/hermes-node/bin/npx  /root/.local/bin/npx && \
    mv /opt/data/bin/uv /opt/data/bin/uvx /usr/local/bin/

# 显式预装 weixin platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      aiohttp cryptography qrcode

# 显式预装 feishu / lark platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
# 引擎 gateway/platforms/feishu.py 长连接走 lark_oapi + websockets；扫码注册用 urllib 不依赖 SDK，
# 但实际 WebSocket 运行必须有这两个包，否则飞书平台起不来（线上 pod 出网受限时 lazy 装会失败）。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      lark-oapi==1.5.3 websockets

# oc-entrypoint Python 依赖：pyyaml 用于 manifest 解析；pytest 用于镜像构建期自检；
# jsonschema 校验 manifest/契约。
# 末尾的构建期自检（python -m pytest）用 system python 跑，因此 ocops server/SSE
# 测试（test_ocops_server_*.py、test_ocops_server_sse.py）需要的 starlette + anyio
# 与 starlette.testclient 依赖的 httpx2（starlette 1.x 的 TestClient 优先 httpx2，
# 退而用 httpx 会告 deprecation，故直接装 httpx2 避免告警与未来失效）也必须装到
# system python，否则自检收集 server 测试即 ImportError，docker build 失败。
# python-multipart：starlette 解析 multipart/form-data（skills install 端点上传 zip 用到）
# 的运行时与测试必需包；缺少则 request.form() 抛 AssertionError 导致 skills_install 组测试失败。
# uvicorn 是运行时进程入口（见下方 venv 安装），自检不需要，故不进 system python。
RUN uv pip install --system --no-cache-dir \
      pyyaml pytest jsonschema starlette anyio httpx2 python-multipart

# oc-ops HTTP 服务运行时依赖：装进 hermes venv。oc-ops 容器最终用
# `python -m uvicorn ocops.server:app` 启动；channel 登录复用 venv 内的 weixin SDK，
# 因此 server 必须能跑在 hermes venv —— starlette 提供 ASGI、uvicorn 提供 ASGI
# server、anyio 是 starlette 运行时依赖。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      starlette uvicorn anyio

# 本目录内 COPY（build context = 本 variant 目录）
# 运维能力（channel/cron/kanban/info/doctor）统一由 oc-ops HTTP 服务（ocops.server:app）
# 对外，manager 走 HTTP 调用，不再装独立 oc-* CLI shim；故镜像内只保留容器生命周期
# 必需的 oc-entrypoint、oc-healthcheck，以及容器内 agent 检索知识库用的 oc-kb。
# ocops 契约（schema + SPEC）仍由 ocops 包与构建期契约测试引用，保留 COPY。
COPY oc-entrypoint.py     /usr/local/bin/oc-entrypoint
# entrypoint_helpers.py 是可 import 的辅助函数模块（oc-entrypoint.py 连字符命名无法 import），
# 落点 /usr/local/lib/ 使构建期自检 PYTHONPATH=/usr/local/lib 下 import entrypoint_helpers 可解析。
COPY entrypoint_helpers.py /usr/local/lib/entrypoint_helpers.py
COPY ocops-contract/      /usr/local/lib/ocops/contract/
COPY oc-kb.py             /usr/local/bin/oc-kb
COPY healthcheck.sh       /usr/local/bin/oc-healthcheck
COPY lib/                 /usr/local/lib/oc-entrypoint/lib/
COPY renderer/            /usr/local/lib/oc-entrypoint/renderer/
COPY migrator/            /usr/local/lib/oc-entrypoint/migrator/
COPY tests/               /usr/local/lib/oc-entrypoint/tests/
# oc-ops 核心包：HTTP server（ocops.server:app）承载全部运维逻辑。
# 落点 /usr/local/lib/ocops 使 `import ocops` 在 system python（构建期自检，
# PYTHONPATH=/usr/local/lib）与 hermes venv（运行时 uvicorn）下均可解析。
COPY ocops/               /usr/local/lib/ocops/
# patches/：构建期补丁脚本，给上游 hermes-agent 包注入 oc-manager 扩展端点。
# 落点 /usr/local/lib/oc-entrypoint/patches 与 tests/ 同级，供构建期自检 import。
COPY patches/             /usr/local/lib/oc-entrypoint/patches/
# locales/：oc-manager 自有 i18n overlay（oc.* 文案）。必须与 patches/ 同级落到
# /usr/local/lib/oc-entrypoint/locales，因为 merge_oc_locales.py 按
# `__file__.parent.parent/"locales"/"oc_overlay.yaml"`（即 patches 的兄弟目录）解析 overlay。
COPY locales/             /usr/local/lib/oc-entrypoint/locales/

RUN chmod +x /usr/local/bin/oc-entrypoint /usr/local/bin/oc-kb \
            /usr/local/bin/oc-healthcheck

# 构建期 i18n / 端点补丁按固定顺序执行，任一步骤非零退出即中断 docker build：
#   1) merge_oc_locales：把 locales/oc_overlay.yaml 的 oc.* 文案合并进上游 en/zh catalog；
#   2) patch_i18n_literals：把 gateway 里未走 t() 的英文裸字符串就地替换为 t() 调用。
#      上游 hermes-agent 的 i18n 只覆盖 t() 调用，gateway/run.py 与 gateway/platforms/base.py
#      仍有一批直接发给对话框的英文字面量（关闭/重启通知、会话过大、斜杠命令回复、错误提示、
#      配对/技能提示、超时诊断等），display.language=zh 对其无效，需此补丁就地替换；
#      锚点缺失即 exit 1 让构建失败，避免上游改版后静默漏翻；
#   3) check_oc_i18n_consistency：一致性守卫，校验 oc.* key 在 en/zh 两侧齐全，非零即失败；
#   4) patch_api_server_reload：给 hermes api_server 注入 POST /oc/skills/reload 端点
#      （与 gateway 同进程），成功后 oc-ops 可 POST http://127.0.0.1:8642/oc/skills/reload
#      触发免重启 skill 热加载。
#   5) patch_api_server_sessions：给 hermes api_server 注入 /api/sessions 会话端点
#      （list/create/get/patch/delete/messages/fork/chat/chat_stream，与 6.5 一致），
#      使被 pin 到 5.16 的实例对话页可用；锚点缺失即 exit 1 让构建失败。
RUN set -e; \
    python3 /usr/local/lib/oc-entrypoint/patches/merge_oc_locales.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_i18n_literals.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/check_oc_i18n_consistency.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_api_server_reload.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_api_server_sessions.py

# 镜像身份信息写到固定位置，供 oc-info 读取
ARG OC_IMAGE_VARIANT
ARG OC_BUILD_TS
RUN printf '{"variant":"%s","hermes_upstream_ref":"%s","built_at":"%s"}\n' \
      "$OC_IMAGE_VARIANT" "$HERMES_REF" "$OC_BUILD_TS" \
      > /etc/oc-image.json

ENV OC_IMAGE_VARIANT=${OC_IMAGE_VARIANT}

# 镜像构建期即执行 runtime 自检；任一单元测试失败都会让 docker build 失败。
# PYTHONPATH=/usr/local/lib 让自检 python（system python）能 import ocops，
# 从而收集并运行 tests/ 下的 ocops 测试（含 server/SSE，依赖上面装到 system
# python 的 starlette/anyio/httpx2）。
RUN PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=/usr/local/lib \
      python -m pytest /usr/local/lib/oc-entrypoint/tests/ -v -p no:cacheprovider

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/oc-healthcheck"]

VOLUME /opt/data
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/oc-entrypoint"]
CMD []
