FROM node:22-bookworm-slim

ENV NPM_CONFIG_PREFIX=/opt/npm \
    PATH=/opt/npm/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    OPENCLAW_HOME=/var/lib/openclaw \
    OPENCLAW_NO_ONBOARD=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends bash curl git python3 make g++ ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && npm install -g openclaw@latest \
    && openclaw plugins install "@tencent-weixin/openclaw-weixin" \
    && openclaw config set plugins.entries.openclaw-weixin.enabled true \
    && mkdir -p /workspace /knowledge/org /knowledge/app /var/lib/openclaw \
    && chmod +x /opt/npm/bin/openclaw

COPY healthcheck.sh /usr/local/bin/openclaw-healthcheck
COPY verify-install.sh /usr/local/bin/openclaw-verify-install

RUN chmod +x /usr/local/bin/openclaw-healthcheck /usr/local/bin/openclaw-verify-install \
    && /usr/local/bin/openclaw-verify-install

WORKDIR /workspace

HEALTHCHECK --interval=30s --timeout=10s --retries=3 CMD ["/usr/local/bin/openclaw-healthcheck"]

CMD ["openclaw", "--version"]
