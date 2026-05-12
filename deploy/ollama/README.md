# Ollama 生产部署包

本目录可独立复制到 Ollama 服务器运行。默认 compose 保留 NVIDIA GPU reservation。

## 镜像版本

生产环境不要使用 `latest`。请在 `.env` 中将 `OLLAMA_IMAGE` 改为固定版本标签或镜像 digest，
避免重启或重新拉取时隐式升级。

## origins 配置

`OLLAMA_ORIGINS` 只填写可信的 new-api 访问来源。若确需设置为 `*`，必须确保 Ollama
服务仅在私有网络中暴露，并通过防火墙限制只有 new-api 服务器可以访问 `11434`。

## GPU 前置检查

使用默认 GPU compose 前，服务器需要安装 NVIDIA Container Toolkit，并使用支持 GPU
reservation 的 Docker Compose。启动前先确认容器内可以访问 GPU：

```bash
docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu24.04 nvidia-smi
```

该命令应正常输出 GPU 信息后，再启动本目录的 GPU compose。

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
