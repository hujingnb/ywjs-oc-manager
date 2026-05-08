# OpenClaw runtime 镜像。
# 通过上游官方 install.sh 安装，再装 @tencent-weixin/openclaw-weixin 微信渠道插件。
# Sprint 0 POC 验证：node:24-bookworm-slim + install.sh + python3/build-essential 完整跑通。

FROM node:24-bookworm-slim

# 微信插件需要 python3 + build-essential 编译 native 模块（实测 spawn python3 ENOENT 后修复）。
# curl/ca-certificates/git 是 install.sh 自身的运行依赖。
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        bash curl ca-certificates git python3 build-essential \
    && rm -rf /var/lib/apt/lists/*

# 用上游官方安装脚本，--no-onboard 跳过交互式 onboarding，--no-prompt 强制非交互。
RUN curl -fsSL https://openclaw.ai/install.sh \
    | bash -s -- --no-onboard --no-prompt

# 装微信渠道插件（外部插件，从 clawhub 下载）。安装后 OpenClaw 自动写入
# plugins.entries.openclaw-weixin.enabled=true，但还需要把 id 加到 plugins.allow 白名单
# 才会被 gateway 加载。
RUN openclaw plugins install '@tencent-weixin/openclaw-weixin' \
    && openclaw config set plugins.allow '["openclaw-weixin"]'

# weixin plugin manifest 三个修补，让 OpenClaw gateway 启动时能自动加载 plugin：
#
# 1. channelConfigs：缺它会让 gateway 报 warning 并跳过加载（log `channel plugin
#    manifest declares openclaw-weixin without channelConfigs metadata`，gateway
#    plugins 列表里只有 memory-core，weixin 不存在）。给 minimum 满足结构。
#
# 2. activation.onStartup=true：channel plugin 默认懒加载（仅 channels.start RPC
#    触发时 spawn），导致每次容器 restart 都需要 manager 重新触发 channels login
#    才能收消息。设 onStartup=true 让 gateway 启动时自动 spawn weixin sidecar，
#    配合 weixin token 持久化目录（apps/{id}/weixin bind mount）实现"扫码一次后
#    重启容器免重扫码"。
#
# 3. enabledByDefault=true：与 onStartup 配合，确保新容器没 user 配置时也启用。
#
# 这是上游 plugin 包 v2.3.1 manifest 字段不全的修补，运行时 patch 会触发 plugin
# verify 失败，必须 build-time 做。
#
# `openclaw plugins install` 在不同 OpenClaw 版本可能装到不同路径（实测 2026.4.29
# 装到 /root/.openclaw/extensions/，2026.5.7 改装到别处），用 find 兼容。
RUN MANIFEST=$(find / -name openclaw.plugin.json -path '*openclaw-weixin*' 2>/dev/null | head -1) \
    && echo "weixin manifest: $MANIFEST" \
    && [ -n "$MANIFEST" ] \
    && python3 -c "import json,sys; \
p=sys.argv[1]; \
d=json.load(open(p)); \
d['channelConfigs']={'openclaw-weixin':{'schema':{'type':'object','additionalProperties':True}}}; \
d['activation']={'onStartup':True}; \
d['enabledByDefault']=True; \
open(p,'w').write(json.dumps(d,indent=2))" "$MANIFEST"

# bind mount 目标目录预创建，避免 docker 把根目录权限吞掉。
RUN mkdir -p /workspace /knowledge/org /knowledge/app

COPY healthcheck.sh /usr/local/bin/openclaw-healthcheck
COPY verify-install.sh /usr/local/bin/openclaw-verify-install
RUN chmod +x /usr/local/bin/openclaw-healthcheck /usr/local/bin/openclaw-verify-install \
    && /usr/local/bin/openclaw-verify-install

WORKDIR /workspace

# Sprint 0 POC 实测：plugin 加载约 11s，预留 60s start-period 给首启 + 异常重试 buffer。
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
  CMD ["/usr/local/bin/openclaw-healthcheck"]

# Gateway 默认 bind=auto（loopback），manager 容器编排会通过 `agent docker proxy` 暴露端口，
# 因此这里用 --bind lan（即 0.0.0.0）确保从容器外可达；--allow-unconfigured 跳过 onboarding 校验。
# 上游 --bind 仅接受命名值 loopback/lan/tailnet/auto/custom，IP 字面量会被拒绝。
CMD ["openclaw", "gateway", "--allow-unconfigured", "--bind", "lan"]
