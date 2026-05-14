#!/bin/sh
# Dockerfile HEALTHCHECK 入口:Hermes 没有 HTTP /healthz,但提供 gateway status CLI。
# 退出码:0 = healthy(gateway 进程在,platform 连接 OK);非 0 = unhealthy。
exec hermes gateway status >/dev/null 2>&1
