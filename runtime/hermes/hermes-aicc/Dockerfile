# runtime/hermes/hermes-v2026.7.1/Dockerfile
# hermes-aicc 客服专用镜像构建脚本。build context = 本目录，COPY 不跨目录。
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

# PyPI 源替换为国内镜像（与上面 apt 用 aliyun 保持一致）。同时作用于两类安装：
#   - 构建期 `uv pip install`（下面预装 weixin/feishu/discord/edge-tts 等）→ 走 UV_INDEX_URL；
#   - 运行期引擎 tools.lazy_deps 的 `python -m pip install`（任何未预装的按需依赖）→ 走 PIP_INDEX_URL。
# 用 ENV 而非仅 ARG：使配置持久到运行容器，让线上出网受限的 pod 内 lazy install 也走国内源，
# 避免直连 pypi.org 慢/超时（本次「验证连接中」根因即出网慢导致 lazy 安装阻塞数分钟）。
ARG PYPI_INDEX_URL=https://mirrors.aliyun.com/pypi/simple/
ENV PIP_INDEX_URL=${PYPI_INDEX_URL} \
    UV_INDEX_URL=${PYPI_INDEX_URL} \
    NPM_CONFIG_REGISTRY=https://registry.npmmirror.com

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
ARG NODE_VERSION=22.23.1
ARG NODE_MIRROR=https://npmmirror.com/mirrors/node
ARG GITHUB_MIRROR=https://ghfast.top/https://github.com/
RUN set -e; \
    node_archive="node-v${NODE_VERSION}-linux-x64.tar.xz"; \
    curl -fsSL "${NODE_MIRROR}/v${NODE_VERSION}/${node_archive}" -o "/tmp/${node_archive}"; \
    mkdir -p /opt/data/node; \
    tar -xJf "/tmp/${node_archive}" --strip-components=1 -C /opt/data/node; \
    rm -f "/tmp/${node_archive}"; \
    /opt/data/node/bin/node --version; \
    git config --global url."${GITHUB_MIRROR}".insteadOf https://github.com/; \
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
      aiohttp cryptography qrcode ddgs

# 显式预装 feishu / lark platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
# 引擎 gateway/platforms/feishu.py 长连接走 lark_oapi + websockets；扫码注册用 urllib 不依赖 SDK，
# 但实际 WebSocket 运行必须有这两个包，否则飞书平台起不来（线上 pod 出网受限时 lazy 装会失败）。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      lark-oapi==1.5.3 websockets

# 显式预装 dingtalk platform 必需依赖（容器启动即 ready，不允许运行时 lazy install）。
# 引擎 gateway/platforms/dingtalk.py 走官方 dingtalk-stream WebSocket 长连接（DINGTALK_CLIENT_ID/
# DINGTALK_CLIENT_SECRET 启用），依赖 dingtalk-stream(>=0.20)+httpx；未预装会在首次启用时
# lazy_deps.ensure("platform.dingtalk") 同步阻塞拉取，线上 pod 出网受限时装不上、起不来
# （与 weixin/feishu/discord 预装同因）。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      "dingtalk-stream>=0.20" httpx

# 显式预装 discord platform 依赖（容器启动即 ready，不允许运行时 lazy install）。
# gateway 启动期平台发现会 import discord 平台模块，触发 tools.lazy_deps 在运行时
# pip 安装 discord.py[voice]+brotlicffi；该安装是同步阻塞——线上 pod 出网慢时实测耗
# 时近 9 分钟，把排在后面的 api_server(8642) 与各消息平台连接全堵在后面，导致此期间
# oc-ops 查 /health/detailed 连接被拒、渠道绑定长时间卡在「验证连接中」。这些实例并不
# 使用 discord，预装只为消除启动期 lazy install 的阻塞与出网失败风险（与上面 weixin/
# feishu 预装同因）。版本号对齐引擎 lazy_deps 声明的 feature 'platform.discord'。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      "discord.py[voice]==2.7.1" brotlicffi==1.2.0.1

# 显式预装 edge-tts（容器启动即 ready，不允许运行时 lazy install）。
# 与 discord 不同，tts.edge 不在启动期触发，而是首次生成语音回复时才 lazy 安装；
# 但出网慢时这次按需安装会让首条语音消息延迟数十秒（实测 1~51s），且线上出网受限
# 时可能失败。预装消除首条语音回复的冷启动延迟。版本号对齐引擎 lazy_deps 声明的
# feature 'tts.edge'。
RUN uv pip install --python /usr/local/lib/hermes-agent/venv/bin/python --no-cache-dir \
      edge-tts==7.2.7

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
COPY oc-publish.py        /usr/local/bin/oc-publish
COPY healthcheck.sh       /usr/local/bin/oc-healthcheck
# AICC 工具策略与知识检索工具安装到 Hermes 包同级目录，model_tools.py 可直接导入。
# 该目录不包含 terminal/file/process 等通用执行入口，仅保留客服只读工具。
COPY aicc_tools/          /usr/local/lib/hermes-agent/aicc_tools/
# Hermes 运行时的工具目录保留同一份知识工具源码，便于镜像审计直接确认 AICC toolset 的实现；
# model_tools 的构建期补丁从 aicc_tools 包显式注册它，避免通用自动发现绕过能力策略。
COPY aicc_tools/aicc_knowledge_tool.py /usr/local/lib/hermes-agent/tools/aicc_knowledge_tool.py
# 上游安装器会带入通用 Skill，其中可能包含文件、终端、发布等与客服无关的操作说明。
# AICC 只保留构建期审核的三个只读 Skill，运行期 renderer 也不会再解压 manifest Skill。
# install.sh 可能把通用 Skill 写进 /opt/data/skills。即使 Docker 匿名卷会初始化该目录，
# 也必须在构建层清空，防止未挂载 emptyDir 的运行形态扫描到旧通用 Skill。
RUN rm -rf /opt/data/skills /opt/oc-aicc-skills && mkdir -p /opt/oc-aicc-skills
COPY skills/ /opt/oc-aicc-skills/
# 构建期测试按源码同级路径读取技能和入口脚本；保留一份只读测试副本，运行时仍只从
# /opt/oc-aicc-skills 同步到 emptyDir，不会扩大客服可见的 Skill 面。
COPY skills/              /usr/local/lib/oc-entrypoint/skills/
COPY oc-entrypoint.py     /usr/local/lib/oc-entrypoint/oc-entrypoint.py
# 该副本仅供镜像内自检读取 Dockerfile 的安全构建顺序，不参与运行时执行。
COPY Dockerfile           /usr/local/lib/oc-entrypoint/Dockerfile
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
            /usr/local/bin/oc-publish /usr/local/bin/oc-healthcheck

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
RUN set -e; \
    python3 /usr/local/lib/oc-entrypoint/patches/merge_oc_locales.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_i18n_literals.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/check_oc_i18n_consistency.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_api_server_reload.py; \
    python3 /usr/local/lib/oc-entrypoint/patches/patch_aicc_tool_policy.py; \
    python3 -m py_compile /usr/local/lib/hermes-agent/model_tools.py

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
RUN PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=/usr/local/lib:/usr/local/lib/hermes-agent \
      python -m pytest /usr/local/lib/oc-entrypoint/tests/ -v -p no:cacheprovider

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/oc-healthcheck"]

VOLUME /opt/data
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/oc-entrypoint"]
CMD []
