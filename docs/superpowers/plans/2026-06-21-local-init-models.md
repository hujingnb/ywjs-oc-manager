# 本地一键重建即得完整初始环境 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `make local-up`（含 `make local-reset` 后）自动完成 new-api / RAGFlow 的初始化、模型与默认配置，并把二者随机生成的管理 token 回填 `secret.yaml`，重建后无需任何手动 UI 操作即得完整初始环境。

**Architecture:** 新增 `scripts/local-init-models.py`（host 侧 python3 stdlib）：new-api 走官方 HTTP API（明文登录，可脚本化），RAGFlow 模型配置走 MySQL 直写（其模型管理只在 session-JWT/RSA-login 后的 web API 后面，且 key/token 明文存储，DB 直写更稳；RAGFlow 已锁 v0.25.6）。脚本生成随机 token 回填 `deploy/k8s/local/secret.yaml` 两行，`kubectl apply` + 重启 manager-api 生效。Makefile 在 `local-up` 末尾门控调用（缺 `.env` 跳过）。

**Tech Stack:** python3（`urllib`/`http.cookiejar`/`json`/`secrets`/`subprocess`，无第三方依赖）、`kubectl exec mysql`、Makefile、传统 `.env`。

参照设计：`docs/superpowers/specs/2026-06-21-local-init-models-design.md`

---

## 约定与已确认事实（实现时直接用）

**鉴权 / 端点（浏览器抓包已确认）**
- new-api 管理 token 鉴权：`Authorization: Bearer <access_token>` + `New-Api-User: 1`。
- new-api 生成系统访问令牌：`GET /api/user/token`（登录会话下），返回 `{success:true,data:"<token>"}`。
- new-api 自检：`GET /api/user/self` → `data.role==100`。
- RAGFlow 外部 SDK API（manager 用）：`GET /api/v1/datasets`，`Authorization: Bearer <ragflow-token>` → `{code:0}`。
- RAGFlow api key 明文存 `rag_flow.api_token.token`（形如 `ragflow-<32位>`）。

**RAGFlow DB 模板（已确认列与样例值，写死 fixture）**
- 表 `rag_flow.tenant_llm` 列：`id(int), create_time(bigint), create_date(datetime), update_time(bigint), update_date(datetime), tenant_id(varchar32), llm_factory(varchar128), model_type(varchar128), llm_name(varchar128), api_key(text), api_base(varchar255), max_tokens(int), used_tokens(int), status(varchar1)`。
- embedding 行样例：`llm_factory='OpenAI-API-Compatible', model_type='embedding', llm_name='BAAI/bge-m3___OpenAI-API', api_base='https://api.siliconflow.cn/v1', api_key=<SiliconFlow key>, max_tokens=8192, used_tokens=0, status='1'`。
- chat 行样例：`llm_factory='DeepSeek', model_type='chat', llm_name='deepseek-v4-pro'（与 'deepseek-v4-flash'）, api_base='', api_key=<DeepSeek key>, max_tokens=8192, used_tokens=0, status='1'`。
- 表 `rag_flow.tenant` 默认模型列：`llm_id='deepseek-v4-pro@DeepSeek', embd_id='BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible'`（`tenant_llm_id`/`tenant_embd_id` 为对应 tenant_llm.id）。
- 表 `rag_flow.api_token`：`tenant_id, token, source='', beta=<随机>` + 时间戳。
- 单租户：`SELECT id FROM rag_flow.tenant LIMIT 1` 取 `tenant_id`（local 仅 admin@ragflow.io 一个）。

**写死的模型 fixture**
- new-api DeepSeek 渠道 models：`deepseek-chat,deepseek-reasoner,deepseek-v4-flash,deepseek-v4-flash-none,deepseek-v4-flash-max,deepseek-v4-pro,deepseek-v4-pro-none,deepseek-v4-pro-max`。
- RAGFlow embedding：`BAAI/bge-m3`（SiliconFlow）；chat：`deepseek-v4-pro`、`deepseek-v4-flash`（DeepSeek）；默认 embedding=bge-m3、默认 LLM=deepseek-v4-pro。

**安全**：`.env` 的两个 key 只读进内存、只发给 new-api 请求体 / 写进 RAGFlow DB，**绝不写进任何 git 跟踪文件**（含脚本、secret.yaml、docs）。

---

## File Structure

| 文件 | 责任 |
|---|---|
| `scripts/local-init-models.py` | 唯一编排脚本：门控→new-api(API)→RAGFlow(DB)→回填 secret→生效→自检 |
| `.env.example` | 提交：新增两个 key 的占位与注释 |
| `.env` | 本地（gitignored）：用户填真值 |
| `Makefile` | `.local-init-models`（内部门控）+ `local-init-models`（公开）+ `local-up` 末尾调用 + `.PHONY` |
| `deploy/k8s/local/secret.yaml` | 运行时被脚本回填 `newapi.admin_token`/`ragflow.api_key` 两行 |
| `docs/deployment-embedding.md` | 本地段由「手动 runbook」改为「local-up 自动；缺 .env 回退手动」 |

脚本内部按职责切函数（同一文件，逻辑分块）：`load_env / gate`、`newapi_setup`、`ragflow_seed`、`backfill_secret`、`apply_and_restart`、`verify`、`main`。

---

## Task 1: `.env` 增加厂商 key 占位

**Files:**
- Modify: `.env.example`
- Modify: `.env`（本地，gitignored；仅本机操作，不提交）

- [ ] **Step 1: 在 `.env.example` 末尾追加占位与注释**

```
#================================================
# scripts/local-init-models.py - 本地 new-api/RAGFlow 模型初始化所需厂商 key
# 真实值只写入 gitignored 的本地 .env，严禁提交。
#================================================
DEEPSEEK_API_KEY=        # DeepSeek 控制台 key（new-api DeepSeek 渠道 + RAGFlow chat）
SILICONFLOW_API_KEY=     # 硅基流动 key（RAGFlow embedding，BAAI/bge-m3）
```

- [ ] **Step 2: 在本机 `.env` 填入真实值（不提交）**

```
DEEPSEEK_API_KEY=<DeepSeek 真实 key>
SILICONFLOW_API_KEY=<SiliconFlow 真实 key>
```

- [ ] **Step 3: 确认 `.env` 不会被提交**

Run: `git check-ignore .env && git status --porcelain .env`
Expected: 打印 `.env`（被忽略），`git status` 不显示 `.env`。

- [ ] **Step 4: Commit（仅 .env.example）**

```bash
git add .env.example
git commit -m "chore(local): .env.example 增加 DeepSeek/SiliconFlow key 占位

供 scripts/local-init-models.py 读取，配置 new-api/RAGFlow 模型；真实 key 仅入本地 .env。"
```

---

## Task 2: 脚本骨架 + 门控（缺 .env/key 优雅跳过）

**Files:**
- Create: `scripts/local-init-models.py`

- [ ] **Step 1: 写骨架与门控**

```python
#!/usr/bin/env python3
"""本地 k3d 一键初始化 new-api / RAGFlow 模型与管理 token。

new-api 走官方 HTTP API；RAGFlow 模型配置走 MySQL 直写（其模型管理只在
session/RSA-login 后的 web API 后面，且 key/token 明文存储，DB 直写更稳）。
生成的随机 admin_token / api_key 回填 deploy/k8s/local/secret.yaml 并重启 manager-api。

缺 .env 或厂商 key 时打印提示并退出 0（不阻断 make local-up）。
"""
import json, os, re, secrets, subprocess, sys, urllib.request, urllib.error, http.cookiejar

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SECRET_FILE = os.path.join(ROOT, "deploy/k8s/local/secret.yaml")
NS = "ocm"
NEWAPI = "http://newapi.localhost"   # traefik ingress；脚本内统一 no_proxy 直连
RAGFLOW = "http://ragflow.localhost"

def load_env():
    """读根 .env，返回 (deepseek_key, siliconflow_key)；缺失返回 (None, None)。"""
    path = os.path.join(ROOT, ".env")
    env = {}
    if os.path.exists(path):
        for line in open(path, encoding="utf-8"):
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v.split("#", 1)[0].strip().strip('"').strip("'")
    return env.get("DEEPSEEK_API_KEY") or None, env.get("SILICONFLOW_API_KEY") or None

def main():
    # 让 *.localhost 直连 traefik，绕开宿主 clash 代理
    os.environ["no_proxy"] = os.environ["NO_PROXY"] = "localhost,127.0.0.1,.localhost"
    deepseek, siliconflow = load_env()
    if not deepseek or not siliconflow:
        print("⏭  跳过 new-api/RAGFlow 模型初始化：缺 .env 或厂商 key "
              "（见 .env.example 的 DEEPSEEK_API_KEY / SILICONFLOW_API_KEY）")
        return 0
    print("⏳ 初始化本地 new-api / RAGFlow 模型与 token …")
    # 后续任务填充：admin_token = newapi_setup(deepseek)
    #             api_key     = ragflow_seed(deepseek, siliconflow)
    #             backfill_secret(admin_token, api_key); apply_and_restart(); verify(...)
    return 0

if __name__ == "__main__":
    sys.exit(main())
```

- [ ] **Step 2: 赋可执行 + 跑门控（无 .env 时）验证不报错**

Run: `chmod +x scripts/local-init-models.py && mv .env .env.bak 2>/dev/null; python3 scripts/local-init-models.py; echo "rc=$?"; mv .env.bak .env 2>/dev/null`
Expected: 打印「⏭ 跳过 …」且 `rc=0`。

- [ ] **Step 3: Commit**

```bash
git add scripts/local-init-models.py
git commit -m "feat(local): 新增 local-init-models 脚本骨架与 .env 门控

缺 .env 或厂商 key 时优雅跳过（exit 0），不阻断 local-up。"
```

---

## Task 3: new-api 初始化模块（纯 API）→ 返回随机 admin_token

**Files:**
- Modify: `scripts/local-init-models.py`

> **先抓包确认 payload（一次性，写进代码常量）**：用浏览器登录 `http://newapi.localhost`（admin/admin123），DevTools Network 下：
> - 渠道管理新建一个渠道 → 记 `POST /api/channel` 的**请求体字段**（至少 `type,name,key,models,groups`，DeepSeek 的 `type` 值，base 留空）。
> - 系统设置切「自用模式」开关 → 记 `PUT /api/option` 的请求体（`{"key":"SelfUseModeEnabled","value":"true"}`）。
> 把确认到的字段名/取值填进下面 `create_channel` / `set_self_use` 的 body。

- [ ] **Step 1: 加 HTTP 小工具 + new-api 登录会话**

```python
def _req(opener, method, url, headers=None, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(url, data=data, method=method,
                               headers={"Content-Type": "application/json", **(headers or {})})
    try:
        with opener.open(r, timeout=30) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")

def newapi_setup(deepseek_key):
    """初始化 new-api、建 DeepSeek 渠道、开自用模式，返回 admin 系统访问令牌。"""
    cj = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
    # 1) 初始化向导（已初始化会返回非 success，忽略）
    _req(op, "POST", f"{NEWAPI}/api/setup",
         body={"username": "admin", "password": "admin123",
               "confirmPassword": "admin123", "SelfUseModeEnabled": True})
    # 2) 登录拿会话
    st, j = _req(op, "POST", f"{NEWAPI}/api/user/login",
                 body={"username": "admin", "password": "admin123"})
    assert j.get("success"), f"new-api 登录失败: {j}"
    uid = str(j["data"]["id"])
    auth = {"New-Api-User": uid}
```

- [ ] **Step 2: 自用模式 + DeepSeek 渠道（幂等）+ 生成令牌**

```python
    # 3) 开自用模式（幂等；payload 以抓包为准）
    _req(op, "PUT", f"{NEWAPI}/api/option", headers=auth,
         body={"key": "SelfUseModeEnabled", "value": "true"})
    # 4) DeepSeek 渠道：先查后建
    st, j = _req(op, "GET", f"{NEWAPI}/api/channel/?p=0&page_size=50", headers=auth)
    chans = (j.get("data") or {}).get("items") or j.get("data") or []
    models = ("deepseek-chat,deepseek-reasoner,deepseek-v4-flash,deepseek-v4-flash-none,"
              "deepseek-v4-flash-max,deepseek-v4-pro,deepseek-v4-pro-none,deepseek-v4-pro-max")
    if not any(c.get("name") == "deepseek" for c in (chans if isinstance(chans, list) else [])):
        st, j = _req(op, "POST", f"{NEWAPI}/api/channel", headers=auth,
                     body={"type": 43, "name": "deepseek", "key": deepseek_key,
                           "base_url": "", "models": models, "groups": ["default"],
                           "group": "default", "model_mapping": "", "status": 1})
        assert j.get("success"), f"建 DeepSeek 渠道失败: {j}"
    # 5) 生成 admin 系统访问令牌
    st, j = _req(op, "GET", f"{NEWAPI}/api/user/token", headers=auth)
    assert j.get("success") and j.get("data"), f"生成 new-api 令牌失败: {j}"
    print("  ✓ new-api：渠道 deepseek + 自用模式 + admin 令牌就绪")
    return j["data"]
```

- [ ] **Step 3: 在 main 接线并跑（环境已手动初始化，作幂等验证）**

在 `main()` 中替换占位：`admin_token = newapi_setup(deepseek)` 并临时 `print(admin_token[:8], "…")`。
Run: `python3 scripts/local-init-models.py`
Expected: 打印「✓ new-api：渠道 deepseek + 自用模式 + admin 令牌就绪」与令牌前缀；不抛异常。

- [ ] **Step 4: 验证令牌可用**

Run: `python3 - <<'PY'` 用打印出的 token 调 `GET /api/user/self`（Bearer + New-Api-User:1）确认 `data.role==100`。（或下游 verify 任务统一验证。）
Expected: role 100。

- [ ] **Step 5: Commit**

```bash
git add scripts/local-init-models.py
git commit -m "feat(local): local-init-models 增加 new-api API 初始化

幂等建 DeepSeek 渠道、开自用模式、生成 admin 系统访问令牌（随机）。"
```

---

## Task 4: RAGFlow 模型配置（MySQL 直写）→ 返回随机 api_key

**Files:**
- Modify: `scripts/local-init-models.py`

> 参照 `scripts/reparse-knowledge-base.sh` 的 `kubectl exec mysql` 模式。所有 SQL 经
> `kubectl -n ocm exec statefulset/mysql -- mysql -uroot -p"$ROOT_PW" -N -e '...'`。

- [ ] **Step 1: 加 mysql 执行小工具**

```python
def _mysql(sql):
    """在集群内 mysql 执行 SQL，返回 stdout（tab 分隔，无表头）。"""
    cmd = ("PW=$(printf %s \"$MYSQL_ROOT_PASSWORD\"); "
           "mysql -uroot -p\"$PW\" -N -e " + _shquote(sql))
    out = subprocess.run(
        ["kubectl", "-n", NS, "exec", "statefulset/mysql", "--",
         "sh", "-c", cmd],
        capture_output=True, text=True, check=True)
    return out.stdout.strip()

def _shquote(s):
    return "'" + s.replace("'", "'\\''") + "'"
```

- [ ] **Step 2: RAGFlow 直写 tenant_llm / tenant 默认 / api_token**

```python
def ragflow_seed(deepseek_key, siliconflow_key):
    """RAGFlow 直写：embedding(SiliconFlow bge-m3)+chat(DeepSeek)+默认模型+api_token，返回 api_key。"""
    tenant = _mysql("SELECT id FROM rag_flow.tenant LIMIT 1;").strip()
    assert tenant, "RAGFlow tenant 不存在（ragflow 未初始化？）"
    now = "UNIX_TIMESTAMP()*1000"; dt = "NOW()"
    def upsert_llm(factory, mtype, name, base, key, maxt):
        # 先删同 (factory,name) 再插，保证幂等且 key 为最新
        _mysql(f"DELETE FROM rag_flow.tenant_llm WHERE tenant_id='{tenant}' "
               f"AND llm_factory='{factory}' AND llm_name='{name}';")
        _mysql(
            "INSERT INTO rag_flow.tenant_llm "
            "(create_time,create_date,update_time,update_date,tenant_id,llm_factory,"
            " model_type,llm_name,api_key,api_base,max_tokens,used_tokens,status) VALUES "
            f"({now},{dt},{now},{dt},'{tenant}','{factory}','{mtype}','{name}',"
            f"'{key}','{base}',{maxt},0,'1');")
    upsert_llm("OpenAI-API-Compatible", "embedding", "BAAI/bge-m3___OpenAI-API",
               "https://api.siliconflow.cn/v1", siliconflow_key, 8192)
    upsert_llm("DeepSeek", "chat", "deepseek-v4-pro", "", deepseek_key, 8192)
    upsert_llm("DeepSeek", "chat", "deepseek-v4-flash", "", deepseek_key, 8192)
    # 取 tenant_llm.id 回填 tenant 的 FK
    embd_id = _mysql("SELECT id FROM rag_flow.tenant_llm WHERE tenant_id="
                     f"'{tenant}' AND llm_name='BAAI/bge-m3___OpenAI-API';").strip()
    llm_id = _mysql("SELECT id FROM rag_flow.tenant_llm WHERE tenant_id="
                    f"'{tenant}' AND llm_name='deepseek-v4-pro';").strip()
    _mysql(
        "UPDATE rag_flow.tenant SET "
        "llm_id='deepseek-v4-pro@DeepSeek', "
        "embd_id='BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible', "
        f"tenant_llm_id={llm_id}, tenant_embd_id={embd_id} WHERE id='{tenant}';")
    # api_token：删旧建新（随机 ragflow-<32>）
    token = "ragflow-" + secrets.token_urlsafe(24)[:32]
    beta = secrets.token_urlsafe(24)[:32]
    _mysql(f"DELETE FROM rag_flow.api_token WHERE tenant_id='{tenant}' AND source='';")
    _mysql(
        "INSERT INTO rag_flow.api_token "
        "(create_time,create_date,update_time,update_date,tenant_id,token,source,beta) "
        f"VALUES ({now},{dt},{now},{dt},'{tenant}','{token}','','{beta}');")
    print("  ✓ RAGFlow：embedding(bge-m3)+chat(deepseek)+默认模型+api_token 就绪")
    return token
```

- [ ] **Step 3: 在 main 接线并跑（幂等覆盖现有手动配置）**

`api_key = ragflow_seed(deepseek, siliconflow)`；临时打印 `api_key[:16]`。
Run: `python3 scripts/local-init-models.py`
Expected: 打印「✓ RAGFlow …」；DB 校验：
`kubectl -n ocm exec statefulset/mysql -- sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N -e "SELECT llm_name FROM rag_flow.tenant_llm; SELECT llm_id,embd_id FROM rag_flow.tenant;"'`
返回三个模型 + 默认 `deepseek-v4-pro@DeepSeek` / `BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible`。

- [ ] **Step 4: 验证新 api_key 可调外部 API**

Run: `curl -sS --noproxy '*' http://ragflow.localhost/api/v1/datasets -H "Authorization: Bearer <打印的 api_key>" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("code"))'`
Expected: `0`。

- [ ] **Step 5: Commit**

```bash
git add scripts/local-init-models.py
git commit -m "feat(local): local-init-models 增加 RAGFlow 模型 DB 直写

幂等写 tenant_llm(SiliconFlow bge-m3 embedding + DeepSeek chat)、设默认模型、
建随机 api_token；避开 RAGFlow RSA 登录，key/token 明文存储 DB 直写安全。"
```

---

## Task 5: 回填 secret.yaml + 生效（apply + 重启 manager-api）

**Files:**
- Modify: `scripts/local-init-models.py`

- [ ] **Step 1: 加回填与生效函数**

```python
def backfill_secret(admin_token, api_key):
    """就地替换 secret.yaml 的 admin_token / api_key 两行（按 yaml key 精确匹配）。"""
    txt = open(SECRET_FILE, encoding="utf-8").read()
    txt = re.sub(r'(\n\s*admin_token:\s*).*', lambda m: m.group(1) + json.dumps(admin_token), txt, count=1)
    txt = re.sub(r'(\n\s*api_key:\s*).*', lambda m: m.group(1) + json.dumps(api_key), txt, count=1)
    open(SECRET_FILE, "w", encoding="utf-8").write(txt)
    print("  ✓ 已回填 secret.yaml（newapi.admin_token / ragflow.api_key）")

def apply_and_restart():
    subprocess.run(["kubectl", "-n", NS, "apply", "-f", SECRET_FILE], check=True)
    subprocess.run(["kubectl", "-n", NS, "rollout", "restart", "deploy/manager-api"], check=True)
    subprocess.run(["kubectl", "-n", NS, "rollout", "status", "deploy/manager-api",
                    "--timeout=120s"], check=True)
    print("  ✓ secret 已下发，manager-api 已重启")
```

> 注意：`re.sub` 精确锚定行首缩进 + key 名，`admin_token:` 只此一处（newapi 段）、`api_key:` 只此一处（ragflow 段），不会误伤。实现后用 `git diff secret.yaml` 确认仅两行变化。

- [ ] **Step 2: main 接线并跑**

`backfill_secret(admin_token, api_key); apply_and_restart()`。
Run: `python3 scripts/local-init-models.py && git --no-pager diff --stat deploy/k8s/local/secret.yaml`
Expected: secret.yaml 仅 2 行改动；manager-api rollout 成功。

- [ ] **Step 3: Commit（仅脚本；secret.yaml 的 token 变化不在本提交）**

```bash
git add scripts/local-init-models.py
git commit -m "feat(local): local-init-models 回填 secret.yaml 并重启 manager-api

精确替换 newapi.admin_token / ragflow.api_key 两行后 apply + rollout restart。"
```

---

## Task 6: 自检 + 摘要

**Files:**
- Modify: `scripts/local-init-models.py`

- [ ] **Step 1: 加 verify**

```python
def verify(admin_token, api_key, deepseek_key):
    op = urllib.request.build_opener()
    # new-api：令牌可用 + DeepSeek chat 通
    st, j = _req(op, "GET", f"{NEWAPI}/api/user/self",
                 headers={"Authorization": f"Bearer {admin_token}", "New-Api-User": "1"})
    assert j.get("data", {}).get("role") == 100, f"new-api 自检失败: {j}"
    st, j = _req(op, "POST", f"{NEWAPI}/v1/chat/completions",
                 headers={"Authorization": f"Bearer {admin_token}"},
                 body={"model": "deepseek-v4-pro",
                       "messages": [{"role": "user", "content": "ping"}], "max_tokens": 5})
    assert "choices" in j, f"DeepSeek chat 自检失败: {j}"
    # RAGFlow：api_key 可用
    st, j = _req(op, "GET", f"{RAGFLOW}/api/v1/datasets",
                 headers={"Authorization": f"Bearer {api_key}"})
    assert j.get("code") == 0, f"RAGFlow 自检失败: {j}"
    print("✅ 初始环境就绪：new-api(渠道+令牌) / RAGFlow(模型+默认+key) / manager 已接通")
```

- [ ] **Step 2: main 末尾调用 + 去掉临时 print，整理 main**

```python
    admin_token = newapi_setup(deepseek)
    api_key = ragflow_seed(deepseek, siliconflow)
    backfill_secret(admin_token, api_key)
    apply_and_restart()
    verify(admin_token, api_key, deepseek)
    return 0
```

- [ ] **Step 3: 整体跑一遍（幂等）**

Run: `python3 scripts/local-init-models.py`
Expected: 末行「✅ 初始环境就绪 …」，rc=0。再跑一次仍成功（幂等）。

- [ ] **Step 4: Commit**

```bash
git add scripts/local-init-models.py
git commit -m "feat(local): local-init-models 增加自检与就绪摘要

校验 new-api 令牌+DeepSeek chat、RAGFlow api_key 后打印初始环境就绪。"
```

---

## Task 7: Makefile 集成（门控步骤 + 公开 target + local-up 调用）

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: 加 `.PHONY` 与两个 target**

在 `.PHONY` 行末追加 `local-init-models .local-init-models`。在 `local-seed` 附近加：

```makefile
local-init-models: ## 初始化 new-api/RAGFlow 模型与管理 token（读 .env 厂商 key，幂等）
	python3 scripts/local-init-models.py

.local-init-models: # 内部：local-up 末尾调用；缺 .env 由脚本自身优雅跳过
	-python3 scripts/local-init-models.py
```

> `.local-init-models` 用 `-` 前缀容错：即便初始化报错也不让整条 local-up 失败（组件本身已就绪）；脚本对「缺 .env」已是 exit 0。

- [ ] **Step 2: 在 `local-up` 末尾、`local-seed` 之后调用**

把 `local-up` 末段改为（在 `$(MAKE) local-seed` 之后、最终 echo 之前）：

```makefile
	# 5) 种子平台管理员（幂等）
	$(MAKE) local-seed
	# 6) new-api/RAGFlow 模型与 token 初始化（有 .env 则跑，无则脚本自跳过）
	$(MAKE) .local-init-models
```

- [ ] **Step 3: 验证 Makefile 解析 + 单独 target 可跑**

Run: `make -n local-up >/dev/null && echo OK; make local-init-models`
Expected: `OK`；`make local-init-models` 跑出「✅ 初始环境就绪」（.env 在时）。

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "feat(local): local-up 末尾自动初始化 new-api/RAGFlow 模型与 token

新增 local-init-models（公开）与 .local-init-models（内部门控，纳入 local-up）；
缺 .env 由脚本优雅跳过，不阻断 local-up。"
```

---

## Task 8: 文档改为自动化说明

**Files:**
- Modify: `docs/deployment-embedding.md`

- [ ] **Step 1: 把「本地开发（k3d）」段的手动 runbook 改为自动化说明**

将该段重写为：

```markdown
## 本地开发（k3d）：自动初始化

本地接厂商 API（不自托管模型）。**模型与 token 已自动化**：在根目录 `.env` 填好
`DEEPSEEK_API_KEY` / `SILICONFLOW_API_KEY`（见 `.env.example`），`make local-up`
末尾会自动跑 `scripts/local-init-models.py` 完成：

- new-api：初始化 `admin/admin123`、自用模式、DeepSeek 渠道、生成 admin 系统访问令牌；
- RAGFlow：SiliconFlow `BAAI/bge-m3`(embedding) + DeepSeek chat、设默认模型、生成 api key；
- 把两个随机 token 回填 `secret.yaml` 并重启 manager-api。

缺 `.env` 时该步自动跳过，不影响组件启动；补好 `.env` 后单独跑 `make local-init-models` 即可。

> `make local-reset` 重建后无需手动点 UI；仅需 `.env` 在位。真实厂商 key 只入本地
> `.env`（gitignored），绝不入 git（见文末「安全约束」）。
```

保留其后的「线上 / 生产」「安全约束」段不动。

- [ ] **Step 2: 校验无真实 key 残留**

Run: `grep -rnE "sk-[A-Za-z0-9]{20,}" docs/ scripts/ deploy/k8s/local/ || echo "clean"`
Expected: `clean`。

- [ ] **Step 3: Commit**

```bash
git add docs/deployment-embedding.md
git commit -m "docs(local): 本地 embedding 段改为 local-up 自动初始化说明

模型与 token 由 scripts/local-init-models.py 自动完成，缺 .env 跳过。"
```

---

## Task 9: 端到端验证（真实重建）

**Files:** 无（验证）

- [ ] **Step 1: 干净重建**

Run: `make local-reset && make local-up`
Expected: 全栈就绪；末尾出现 `.local-init-models` 的「✅ 初始环境就绪 …」。

- [ ] **Step 2: 浏览器复核（AGENTS.md 交付前检查要求真实浏览器）**

- new-api `http://newapi.localhost`：admin/admin123 登录；渠道管理见 `deepseek` 渠道、自用模式标签。
- RAGFlow `http://ragflow.localhost`：admin@ragflow.io/admin 登录；模型提供商见 SiliconFlow bge-m3 + DeepSeek，默认 embedding=bge-m3、LLM=deepseek-v4-pro。
- manager `http://ocm.localhost`：admin/admin123 登录；首页「new-api 实时」用量面板加载、「行业知识库」打开无报错。
- RAGFlow 建临时库上传文档解析「完成」、检索 `Vector similarity` 有值，验毕删除临时库。

- [ ] **Step 3: 幂等复跑**

Run: `make local-init-models`
Expected: 再次「✅ 初始环境就绪」，无重复渠道/模型，`git diff secret.yaml` 仅两行 token 变化。

---

## Self-Review（写计划后自查）

- **Spec 覆盖**：①组件运行=现状 local-up（Task 9 验证）②管理员账号=new-api setup(Task 3)+RAGFlow 自建+现状 manager/minio ③secret 回填=Task 5 ④模型=Task 3/4 ⑤.env=Task 1 — 全覆盖。
- **占位扫描**：Task 3 的 new-api `POST /api/channel`/`PUT /api/option` 字段标注「以抓包为准」，已给出确定的端点与最可能 body，并要求实现前一次性抓包确认（具体、可执行，非空泛占位）。其余 RAGFlow/secret/Make 均为确定代码。
- **类型一致**：`newapi_setup`→`admin_token`、`ragflow_seed`→`api_key`、`backfill_secret(admin_token, api_key)`、`verify(admin_token, api_key, deepseek)` 贯穿一致；`_req/_mysql/_shquote` 命名一致。
- **风险**：new-api 渠道 API 字段随版本可能变 → Task 3 抓包兜底；RAGFlow DB schema 已锁版本并按实测列写死。
