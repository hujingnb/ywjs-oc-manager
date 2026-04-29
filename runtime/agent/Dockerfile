FROM debian:12

WORKDIR /app

# 本镜像用于部署已构建好的 agent 二进制；本地开发 compose 仍通过 bind mount + go run 运行源码。
COPY oc-runtime-agent /usr/local/bin/oc-runtime-agent

EXPOSE 7001 7002

CMD ["oc-runtime-agent"]
