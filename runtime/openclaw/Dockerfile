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
