#!/usr/bin/env python3
"""本地 k3d 一键初始化 new-api / RAGFlow 模型与管理 token。

设计动机：
  - new-api 走官方 HTTP API（明文登录可脚本化、对组件升级更稳、不耦合 DB schema）。
  - RAGFlow 模型配置走 MySQL 直写：其模型管理 API 藏在 RSA 登录 + session JWT 之后，
    脚本化困难；而 key/token 在 DB 明文存储，且镜像已锁版本（v0.25.6），DB 直写更稳更安全。
生成的随机 admin_token / api_key 回填 deploy/k8s/local/secret.yaml 两行，并 apply +
重启 manager-api 让 manager 加载新 token。

幂等：可反复执行而不产生重复或报错（先查后建 / 先删后插 / 直接置目标值）。
门控：缺 .env 或厂商 key 时打印中文提示并退出 0（不阻断 make local-up）。

安全约束：.env 的两个厂商 key 只读进内存、只发给 new-api 请求体 / 写进 RAGFlow DB，
绝不写进任何 git 跟踪文件（脚本、secret.yaml、docs 均不得出现真实 key）。
"""
import http.cookiejar
import json
import os
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request

# 仓库根目录（本文件位于 <root>/scripts/ 下）
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
# 待回填的本地 secret 文件（git 跟踪；其 token 两行为运行时产物）
SECRET_FILE = os.path.join(ROOT, "deploy/k8s/local/secret.yaml")
# 本地 k3d 的 manager / 数据库所在 namespace
NS = "ocm"
# new-api / RAGFlow 经 traefik ingress 暴露（*.localhost → 127.0.0.1:80）
NEWAPI = "http://newapi.localhost"
RAGFLOW = "http://ragflow.localhost"

# new-api DeepSeek 渠道承接的模型清单（写死的本地 fixture，与 type=43 DeepSeek 绑定）
DEEPSEEK_MODELS = (
    "deepseek-chat,deepseek-reasoner,deepseek-v4-flash,deepseek-v4-flash-none,"
    "deepseek-v4-flash-max,deepseek-v4-pro,deepseek-v4-pro-none,deepseek-v4-pro-max"
)


def _wait_until(desc, check, timeout=300, interval=5):
    """轮询 check()（返回真值即就绪），超时抛错。

    用于在 make local-up 末尾运行本脚本的场景：local-up 不等 RAGFlow 首次初始化
    （下 tiktoken + 建库建 tenant，较慢），new-api 也可能仍在拉起。check() 内部异常
    （如连接拒绝、表尚不存在）视为「未就绪」继续等。
    """
    deadline = time.monotonic() + timeout
    while True:
        try:
            if check():
                return
        except Exception:
            pass
        if time.monotonic() >= deadline:
            raise RuntimeError(f"等待超时（{timeout}s）：{desc}")
        time.sleep(interval)


def load_env():
    """读根 .env，返回 (deepseek_key, siliconflow_key)；缺失任一返回该位为 None。

    解析极简：跳过空行/注释行，按首个 '=' 切分，去掉行尾 '# 注释' 与首尾引号。
    """
    path = os.path.join(ROOT, ".env")
    env = {}
    if os.path.exists(path):
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                env[k.strip()] = v.split("#", 1)[0].strip().strip('"').strip("'")
    return env.get("DEEPSEEK_API_KEY") or None, env.get("SILICONFLOW_API_KEY") or None


# ----------------------------------------------------------------------------
# new-api：纯 HTTP API
# ----------------------------------------------------------------------------

def _req(opener, method, url, headers=None, body=None):
    """发一次 JSON 请求，返回 (status_code, 解析后的 dict)。

    HTTPError 也读出 body 解析返回（new-api 失败时仍是 JSON），便于上层判 success。
    """
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        url, data=data, method=method,
        headers={"Content-Type": "application/json", **(headers or {})},
    )
    try:
        with opener.open(req, timeout=60) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read() or b"{}")
        except json.JSONDecodeError:
            return e.code, {}


def newapi_setup(deepseek_key):
    """初始化 new-api、开自用模式、幂等建 DeepSeek 渠道，返回 admin 系统访问令牌（随机）。"""
    cj = http.cookiejar.CookieJar()
    op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))

    # 0) 等 new-api API 起来（fresh local-up 时 pod 可能仍在拉起）
    _wait_until("new-api API 就绪",
                lambda: _req(op, "GET", f"{NEWAPI}/api/status")[0] == 200, timeout=180)

    # 1) 初始化向导：已初始化会返回 success=false，幂等忽略
    _req(op, "POST", f"{NEWAPI}/api/setup",
         body={"username": "admin", "password": "admin123",
               "confirmPassword": "admin123", "SelfUseModeEnabled": True})

    # 2) 登录取会话（cookie 由 opener 维持），拿 admin 用户 id 作 New-Api-User 头
    _, j = _req(op, "POST", f"{NEWAPI}/api/user/login",
                body={"username": "admin", "password": "admin123"})
    assert j.get("success"), f"new-api 登录失败: {j}"
    uid = str(j["data"]["id"])
    auth = {"New-Api-User": uid}

    # 3) 开自用模式（幂等；避免请求报「模型价格未配置」）。endpoint 必须带结尾斜杠。
    _req(op, "PUT", f"{NEWAPI}/api/option/", headers=auth,
         body={"key": "SelfUseModeEnabled", "value": "true"})

    # 4) DeepSeek 渠道：先查后建（按 name=="deepseek" 判重，保证幂等）。p 从 1 开始。
    _, j = _req(op, "GET",
                f"{NEWAPI}/api/channel/?p=1&page_size=50&id_sort=false&tag_mode=false",
                headers=auth)
    data = j.get("data")
    chans = data.get("items") if isinstance(data, dict) else data
    chans = chans if isinstance(chans, list) else []
    if not any(c.get("name") == "deepseek" for c in chans):
        # 关键：必须用嵌套 body（mode + channel）且 endpoint 带结尾斜杠，
        # 否则 new-api 会返回 200 却不创建渠道（实测）。
        _, j = _req(op, "POST", f"{NEWAPI}/api/channel/", headers=auth,
                    body={"mode": "single", "channel": {
                        "type": 43, "name": "deepseek", "key": deepseek_key,
                        "base_url": "", "models": DEEPSEEK_MODELS,
                        "groups": ["default"], "group": "default",
                        "model_mapping": "", "priority": 0, "weight": 0,
                        "auto_ban": 1, "multi_key_mode": "random"}})
        assert j.get("success"), f"建 DeepSeek 渠道失败: {j}"

    # 5) 生成 admin 系统访问令牌（随机），作为回填 secret 的 newapi.admin_token
    _, j = _req(op, "GET", f"{NEWAPI}/api/user/token", headers=auth)
    assert j.get("success") and j.get("data"), f"生成 new-api 令牌失败: {j}"
    print("  ✓ new-api：渠道 deepseek + 自用模式 + admin 令牌就绪")
    return j["data"]


# ----------------------------------------------------------------------------
# RAGFlow：MySQL 直写
# ----------------------------------------------------------------------------

def _shquote(s):
    """把字符串安全包成单引号 shell 参数（内部单引号转义），用于 sh -c 传 SQL。"""
    return "'" + s.replace("'", "'\\''") + "'"


def _mysql(sql):
    """在 ocm 的 mysql statefulset 内执行 SQL，返回 stdout（tab 分隔、无表头）。

    口令只用容器内 env $MYSQL_ROOT_PASSWORD，绝不带出容器或写进文件。
    """
    inner = 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -N -e ' + _shquote(sql)
    out = subprocess.run(
        ["kubectl", "-n", NS, "exec", "statefulset/mysql", "--", "sh", "-c", inner],
        capture_output=True, text=True, check=True,
    )
    return out.stdout.strip()


def ragflow_seed(deepseek_key, siliconflow_key):
    """RAGFlow 直写：embedding(SiliconFlow bge-m3) + chat(DeepSeek) + 默认模型 + api_token。

    返回随机 api_key（形如 ragflow-<32>），作为回填 secret 的 ragflow.api_key。
    """
    # 等 RAGFlow 首次初始化完成（建 rag_flow.tenant 表并 seed admin@ragflow.io 租户）。
    # fresh local-up 时 RAGFlow 启动慢（下 tiktoken），tenant 表/行尚不存在时 _mysql 会
    # 抛错，由 _wait_until 视为未就绪继续等。
    _wait_until("RAGFlow 初始化（rag_flow.tenant）",
                lambda: bool(_mysql("SELECT id FROM rag_flow.tenant LIMIT 1;").strip()),
                timeout=420)
    tenant = _mysql("SELECT id FROM rag_flow.tenant LIMIT 1;").strip()
    assert tenant, "RAGFlow tenant 不存在（ragflow 未初始化？）"
    now, dt = "UNIX_TIMESTAMP()*1000", "NOW()"

    def upsert_llm(factory, mtype, name, base, key, maxt):
        # 先删同 (tenant, factory, name) 再插，保证幂等且 api_key 取最新
        _mysql(f"DELETE FROM rag_flow.tenant_llm WHERE tenant_id='{tenant}' "
               f"AND llm_factory='{factory}' AND llm_name='{name}';")
        _mysql(
            "INSERT INTO rag_flow.tenant_llm "
            "(create_time,create_date,update_time,update_date,tenant_id,llm_factory,"
            " model_type,llm_name,api_key,api_base,max_tokens,used_tokens,status) VALUES "
            f"({now},{dt},{now},{dt},'{tenant}','{factory}','{mtype}','{name}',"
            f"'{key}','{base}',{maxt},0,'1');")

    # embedding：SiliconFlow bge-m3（max_tokens 8192，避开 bce 的 512 上限坑）
    upsert_llm("OpenAI-API-Compatible", "embedding", "BAAI/bge-m3___OpenAI-API",
               "https://api.siliconflow.cn/v1", siliconflow_key, 8192)
    # chat：DeepSeek 两档模型
    upsert_llm("DeepSeek", "chat", "deepseek-v4-pro", "", deepseek_key, 8192)
    upsert_llm("DeepSeek", "chat", "deepseek-v4-flash", "", deepseek_key, 8192)

    # 取刚写入的 tenant_llm.id 回填 tenant 的默认模型外键
    embd_id = _mysql(
        "SELECT id FROM rag_flow.tenant_llm WHERE tenant_id="
        f"'{tenant}' AND llm_name='BAAI/bge-m3___OpenAI-API';").strip()
    llm_id = _mysql(
        "SELECT id FROM rag_flow.tenant_llm WHERE tenant_id="
        f"'{tenant}' AND llm_name='deepseek-v4-pro';").strip()
    _mysql(
        "UPDATE rag_flow.tenant SET "
        "llm_id='deepseek-v4-pro@DeepSeek', "
        "embd_id='BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible', "
        f"tenant_llm_id={llm_id}, tenant_embd_id={embd_id} WHERE id='{tenant}';")

    # api_token（外部 SDK key，明文）：删旧建新，随机 ragflow-<32>
    token = "ragflow-" + secrets.token_urlsafe(24)[:32]
    beta = secrets.token_urlsafe(24)[:32]
    _mysql(f"DELETE FROM rag_flow.api_token WHERE tenant_id='{tenant}' AND source='';")
    _mysql(
        "INSERT INTO rag_flow.api_token "
        "(create_time,create_date,update_time,update_date,tenant_id,token,source,beta) "
        f"VALUES ({now},{dt},{now},{dt},'{tenant}','{token}','','{beta}');")
    print("  ✓ RAGFlow：embedding(bge-m3)+chat(deepseek)+默认模型+api_token 就绪")
    return token


# ----------------------------------------------------------------------------
# 回填 secret.yaml 并生效
# ----------------------------------------------------------------------------

def backfill_secret(admin_token, api_key):
    """就地替换 secret.yaml 的 admin_token / api_key 两行（各只一处，精确锚定）。"""
    with open(SECRET_FILE, encoding="utf-8") as fh:
        txt = fh.read()
    # \n + 缩进 + key 名锚定，整行行尾替换为 json.dumps 加引号的新值；count=1 防误伤
    txt = re.sub(r'(\n\s*admin_token:\s*).*',
                 lambda m: m.group(1) + json.dumps(admin_token), txt, count=1)
    txt = re.sub(r'(\n\s*api_key:\s*).*',
                 lambda m: m.group(1) + json.dumps(api_key), txt, count=1)
    with open(SECRET_FILE, "w", encoding="utf-8") as fh:
        fh.write(txt)
    print("  ✓ 已回填 secret.yaml（newapi.admin_token / ragflow.api_key）")


def apply_and_restart():
    """apply 新 secret 并滚动重启 manager-api，让其加载新 token。"""
    subprocess.run(["kubectl", "-n", NS, "apply", "-f", SECRET_FILE], check=True)
    subprocess.run(["kubectl", "-n", NS, "rollout", "restart", "deploy/manager-api"],
                   check=True)
    subprocess.run(["kubectl", "-n", NS, "rollout", "status", "deploy/manager-api",
                    "--timeout=120s"], check=True)
    print("  ✓ secret 已下发，manager-api 已重启")


# ----------------------------------------------------------------------------
# 自检
# ----------------------------------------------------------------------------

def verify(admin_token, api_key):
    """两条通路分开自检：new-api(令牌+DeepSeek 渠道) 与 RAGFlow(api_key)。

    说明：admin_token 是 new-api「系统访问令牌」，仅供管理 API（manager 即用它调管理面），
    它不是 /v1 网关的 API token，故不能拿来调 /v1/chat/completions（会 Invalid token）。
    DeepSeek 渠道健康改用管理面内置的渠道连通性测试 GET /api/channel/test/:id 验证，
    既证明 admin_token 管理权限有效、又证明渠道 + 厂商 key 可用。
    """
    op = urllib.request.build_opener()
    auth = {"Authorization": f"Bearer {admin_token}", "New-Api-User": "1"}
    # new-api：系统令牌可用（role=100 平台管理员）
    _, j = _req(op, "GET", f"{NEWAPI}/api/user/self", headers=auth)
    assert j.get("data", {}).get("role") == 100, f"new-api 自检失败: {j}"
    # 定位 deepseek 渠道 id，再用管理面渠道测试验证 DeepSeek 渠道 + key 连通
    _, j = _req(op, "GET",
                f"{NEWAPI}/api/channel/?p=1&page_size=50&id_sort=false&tag_mode=false",
                headers=auth)
    data = j.get("data")
    chans = data.get("items") if isinstance(data, dict) else data
    chans = chans if isinstance(chans, list) else []
    cid = next((c.get("id") for c in chans if c.get("name") == "deepseek"), None)
    assert cid, f"未找到 deepseek 渠道，无法测连通: {j}"
    _, j = _req(op, "GET",
                f"{NEWAPI}/api/channel/test/{cid}?model=deepseek-v4-pro", headers=auth)
    assert j.get("success"), f"DeepSeek 渠道连通自检失败: {j}"
    # RAGFlow：api_key 可调外部 SDK API
    _, j = _req(op, "GET", f"{RAGFLOW}/api/v1/datasets",
                headers={"Authorization": f"Bearer {api_key}"})
    assert j.get("code") == 0, f"RAGFlow 自检失败: {j}"
    print("✅ 初始环境就绪：new-api(渠道+令牌) / RAGFlow(模型+默认+key) / manager 已接通")


def main():
    # 让 *.localhost 直连 traefik，绕开宿主 clash 代理
    os.environ["no_proxy"] = os.environ["NO_PROXY"] = "localhost,127.0.0.1,.localhost"
    deepseek, siliconflow = load_env()
    if not deepseek or not siliconflow:
        print("⏭  跳过 new-api/RAGFlow 模型初始化：缺 .env 或厂商 key "
              "（见 .env.example 的 DEEPSEEK_API_KEY / SILICONFLOW_API_KEY）")
        return 0
    print("⏳ 初始化本地 new-api / RAGFlow 模型与 token …")
    admin_token = newapi_setup(deepseek)
    api_key = ragflow_seed(deepseek, siliconflow)
    backfill_secret(admin_token, api_key)
    apply_and_restart()
    verify(admin_token, api_key)
    return 0


if __name__ == "__main__":
    sys.exit(main())
