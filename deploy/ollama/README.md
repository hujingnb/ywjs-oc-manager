# Ollama 生产部署包

本目录可独立复制到 Ollama 服务器运行。默认 compose 保留 NVIDIA GPU reservation。

## 启动

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

## 无 GPU 环境

如果服务器没有 NVIDIA GPU，删除 `docker-compose.yml` 中的 `deploy.resources.reservations.devices`
段后再启动。

## 拉取模型

```bash
docker compose exec ollama ollama pull qwen2.5:0.5b
docker compose exec ollama ollama list
```

## 对接 new-api

new-api 后台渠道 base URL 填写：

```text
http://<ollama-host>:11434
```

生产防火墙建议只允许 new-api 服务器访问 `11434`。
