# ollama 生产部署

> 本运行包部署到 Ollama 服务器，提供本地大模型推理。
> Ollama 服务仅供 new-api 调用，不直接对公网暴露。

## 1. 启动

### GPU 环境（默认）

使用前先确认节点已安装 NVIDIA Container Toolkit，并验证 GPU 可用：

```bash
docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu24.04 nvidia-smi
```

输出 GPU 信息后再启动：

```bash
cp .env.example .env
${EDITOR:-vi} .env
docker compose up -d
```

### 无 GPU 环境

删除 `docker-compose.yml` 中的 `deploy.resources.reservations.devices` 段后再启动：

```bash
cp .env.example .env
${EDITOR:-vi} .env
# 编辑 docker-compose.yml，删除 deploy 段
docker compose up -d
```

## 2. 必改配置

### `.env`

| 变量 | 说明 |
|------|------|
| `OLLAMA_IMAGE` | Ollama 镜像，走 `docker.1ms.run/ollama/ollama` 镜像加速；使用具体 tag 或 `@sha256:` digest（禁用 `latest`），避免重启时隐式升级 |
| `OLLAMA_PORT` | 对外服务端口，默认 `11434` |
| `OLLAMA_ORIGINS` | 允许访问的来源，填写 new-api 访问地址，例如 `https://new-api.example.com` |
| `OLLAMA_HOST` | 容器内监听地址，生产保持 `0.0.0.0:11434` |

`OLLAMA_ORIGINS` 只填写可信来源。若确需设置为 `*`，必须确保 11434 端口已通过防火墙限制只有 new-api 服务器可访问。

## 3. 拉取模型

```bash
# 拉取模型（示例）
docker compose exec ollama ollama pull qwen2.5:0.5b

# 查看已下载模型
docker compose exec ollama ollama list
```

模型文件持久化在 `./data/ollama`，容器重建后无需重新拉取。

## 4. 防火墙

| 端口 | 协议 | 允许来源 |
|------|------|----------|
| `11434` (OLLAMA_PORT) | TCP | 仅 new-api 服务器 IP |

生产环境建议在宿主防火墙（iptables / firewalld / 安全组）限制只有 new-api 服务器可以访问 11434，不要对公网开放。

## 5. 对接 new-api

在 new-api 后台「渠道」中添加 Ollama 渠道：

- **类型**：Ollama
- **Base URL**：`http://<ollama-host>:11434`

## 6. 状态检查 / 验证

```bash
# 查看容器状态
docker compose ps

# 检查 Ollama 服务
docker compose exec ollama ollama list

# 从 new-api 服务器测试连通性（在 new-api 主机上执行）
curl http://<ollama-host>:11434/api/tags

# 查看日志
docker compose logs -f --tail=100 ollama
```

## 7. 数据目录

| 路径 | 内容 |
|------|------|
| `./data/ollama` | 模型文件缓存（`~/.ollama` 挂载点） |

模型文件通常较大，迁移前确认磁盘空间充足。

## 8. 常见问题

- **容器无法访问 GPU**：确认已安装 NVIDIA Container Toolkit，并且 Docker daemon 配置了 `nvidia` runtime。
- **拉取模型失败**：检查网络出口是否能访问模型仓库，或配置 HTTP_PROXY 环境变量。
- **new-api 调用 Ollama 超时**：检查防火墙是否放行了 new-api → Ollama 11434 的流量。
