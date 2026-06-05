#!/usr/bin/env bash
# 重新解析「指定知识库」内的全部文档，可选先更换 embedding 模型。
#
# 典型用途——更换知识库的 embedding 模型：
#   RAGFlow 不允许在库内已有 chunk(旧维度向量)时通过 UI 改 embedding 模型——会触发维度预检
#   报错「The dimension (X) ... is different from the original (Y)」。本脚本直接改 RAGFlow
#   knowledgebase.embd_id 绕过该预检，再重解析全部文档。RAGFlow 重解析会逐个文档先按 doc_id
#   删旧 chunk 再插新 chunk，且向量字段按维度命名(q_<dim>_vec)、新旧维度可共存，故能平滑过渡，
#   最终整库收敛到新模型。仅当 knowledgebase.tenant_embd_id 为空时 embd_id 才决定解析所用模型
#   (本脚本会校验)。
#
# 也可只重解析(不带目标模型)，用于模型已就绪、只想让全部文档重跑一遍的场景。
#
# 连接信息(manager 库 / RAGFlow 库 / RAGFlow API)全部运行时从 k8s secret ocm-secrets 解析，
# 不在本文件硬编码任何地址或口令。
#
# 用法：
#   scripts/reparse-knowledge-base.sh <知识库ID> [dry|apply] [目标embd_id] [每批条数]
#     <知识库ID>  manager 的 dataset id(UUID)，或 RAGFlow 的 kb_id，二者皆可
#     dry(默认)   只统计：库信息、文档数、当前/目标模型，不做任何修改
#     apply       执行：若给了目标模型且与当前不同→先改 embd_id；随后重解析全部文档
#     目标embd_id  形如 BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible(留空则不换模型)
#     每批条数     RAGFlow 重解析每批文档数，默认 100
#
# 例：把 fin-sec 库换成 bge-m3 并重解析全部
#   scripts/reparse-knowledge-base.sh 59606263-d92c-4564-9ff9-29221de2aa48 apply \
#     'BAAI/bge-m3___OpenAI-API@OpenAI-API-Compatible'
#
# 注意：重解析整库会让 RAGFlow + embedding 服务承受全量重嵌的负载，请择机执行。
set -euo pipefail

KC="${KUBECONFIG_PATH:-$HOME/dir/ywjs/kube/kubeconfig.json}"   # 生产 kubeconfig，可用 KUBECONFIG_PATH 覆盖
NS="${OCM_NAMESPACE:-ocm}"
SECRET="${OCM_SECRET:-ocm-secrets}"

KB_ARG="${1:-}"
MODE="${2:-dry}"
TARGET_EMBD="${3:-}"
BATCH="${4:-100}"
[ -n "$KB_ARG" ] || { echo "用法: $0 <知识库ID> [dry|apply] [目标embd_id] [每批条数]"; exit 1; }

RAG="$(kubectl --kubeconfig "$KC" -n "$NS" get pod -l app=ragflow -o jsonpath='{.items[0].metadata.name}')"
[ -n "$RAG" ] || { echo "未找到 ragflow pod"; exit 1; }

sec() { kubectl --kubeconfig "$KC" -n "$NS" get secret "$SECRET" -o jsonpath="{.data.$1}" | base64 -d; }

# manager 库连接：从 manager.yaml 的 DSN(mysql://user:pass@tcp(host:port)/db) 解析
MYAML="$(sec 'manager\.yaml')"
DSN="$(printf '%s' "$MYAML" | grep -oP 'url:\s*"\Kmysql://[^"]+')"
M_USER="$(printf '%s' "$DSN" | grep -oP '^mysql://\K[^:]+')"
M_PW="$(printf '%s' "$DSN" | grep -oP '^mysql://[^:]+:\K[^@]+(?=@tcp\()')"
M_HP="$(printf '%s' "$DSN" | grep -oP '@tcp\(\K[^)]+')"; M_HOST="${M_HP%%:*}"; M_PORT="${M_HP##*:}"
M_DB="$(printf '%s' "$DSN" | grep -oP '\)/\K[^?]+')"

# RAGFlow 库连接：独立 secret 键
R_HOST="$(sec ragflow-mysql-host)"; R_PORT="$(sec ragflow-mysql-port)"
R_USER="$(sec ragflow-mysql-user)"; R_DB="$(sec ragflow-mysql-dbname)"; R_PW="$(sec ragflow-mysql-password)"

# RAGFlow API：manager.yaml 的 ragflow 段
RAG_SECTION="$(printf '%s' "$MYAML" | sed -n '/^ragflow:/,/^[a-z_]/p')"
RAG_BASE="$(printf '%s' "$RAG_SECTION" | grep -oP 'base_url:\s*"?\K[^"\s]+' | head -1)"
RAG_KEY="$(printf '%s' "$RAG_SECTION" | grep -oP 'api_key:\s*"?\K[^"\s]+' | head -1)"

for v in M_USER M_PW M_HOST M_PORT M_DB R_HOST R_PORT R_USER R_DB R_PW RAG_BASE RAG_KEY; do
  [ -n "${!v}" ] || { echo "解析连接信息失败：$v 为空"; exit 1; }
done

echo "ragflow pod=$RAG  kb_arg=$KB_ARG  MODE=$MODE  目标模型=${TARGET_EMBD:-（不换）}  每批=$BATCH"

kubectl --kubeconfig "$KC" -n "$NS" exec -i "$RAG" -- env \
  M_HOST="$M_HOST" M_PORT="$M_PORT" M_USER="$M_USER" M_PW="$M_PW" M_DB="$M_DB" \
  R_HOST="$R_HOST" R_PORT="$R_PORT" R_USER="$R_USER" R_PW="$R_PW" R_DB="$R_DB" \
  RAG_BASE="$RAG_BASE" RAG_KEY="$RAG_KEY" \
  KB_ARG="$KB_ARG" MODE="$MODE" TARGET_EMBD="$TARGET_EMBD" BATCH="$BATCH" \
  /ragflow/.venv/bin/python3 - <<'PY'
import os, json, urllib.request, pymysql

kb_arg = os.environ["KB_ARG"].strip()
mode   = os.environ["MODE"]
target = os.environ["TARGET_EMBD"].strip()
batch  = max(1, int(os.environ["BATCH"]))

m = pymysql.connect(host=os.environ["M_HOST"], port=int(os.environ["M_PORT"]), user=os.environ["M_USER"],
                    password=os.environ["M_PW"], database=os.environ["M_DB"], charset="utf8mb4", autocommit=False)
r = pymysql.connect(host=os.environ["R_HOST"], port=int(os.environ["R_PORT"]), user=os.environ["R_USER"],
                    password=os.environ["R_PW"], database=os.environ["R_DB"], charset="utf8mb4", autocommit=False)
mc, rc = m.cursor(), r.cursor()

# 解析知识库标识：先按 manager dataset id 查，查不到再按 RAGFlow kb_id 查
mc.execute("SELECT id, ragflow_dataset_id, scope_type FROM ragflow_datasets WHERE id=%s OR ragflow_dataset_id=%s", (kb_arg, kb_arg))
ds = mc.fetchone()
if not ds:
    print("找不到知识库(既不是 manager dataset id 也不是 ragflow kb_id):", kb_arg); raise SystemExit(1)
manager_ds_id, kb_id, scope = ds
if not kb_id:
    print("该知识库尚无 RAGFlow dataset(ragflow_dataset_id 为空)，无法重解析"); raise SystemExit(1)

rc.execute("SELECT name, tenant_id, embd_id, tenant_embd_id, doc_num, chunk_num FROM knowledgebase WHERE id=%s", (kb_id,))
kb = rc.fetchone()
if not kb:
    print("RAGFlow 中找不到该 kb:", kb_id); raise SystemExit(1)
kb_name, tenant_id, cur_embd, tenant_embd, doc_num, chunk_num = kb

mc.execute("SELECT id, ragflow_document_id FROM ragflow_documents WHERE dataset_id=%s", (manager_ds_id,))
docs = mc.fetchall()

print(f"知识库: {kb_name}")
print(f"  manager dataset = {manager_ds_id}  ragflow kb = {kb_id}  scope = {scope}")
print(f"  文档: manager={len(docs)}  ragflow doc_num={doc_num}  chunk_num={chunk_num}")
print(f"  当前 embedding 模型: {cur_embd}")

will_switch = False
if target:
    if tenant_embd:
        print(f"  ⚠️ 该 kb 的 tenant_embd_id={tenant_embd} 非空，解析模型由它决定而非 embd_id；改 embd_id 不生效，请先处理。")
        raise SystemExit(1)
    t_name = target.rsplit("@", 1)[0]
    rc.execute("SELECT COUNT(*) FROM tenant_llm WHERE tenant_id=%s AND model_type='embedding' AND llm_name=%s", (tenant_id, t_name))
    if rc.fetchone()[0] == 0:
        print(f"  目标模型 {target} 未在该租户注册为 embedding 模型，终止。"); raise SystemExit(1)
    will_switch = (target != cur_embd)
    print(f"  目标 embedding 模型: {target} ({'将切换' if will_switch else '与当前一致，仅重解析'})")

if mode != "apply":
    act = f"切换模型为 {target} 并 " if will_switch else ""
    print(f"\ndry-run：将{act}重解析 {len(docs)} 个文档。未做任何修改。确认后用 apply 执行。")
    m.close(); r.close(); raise SystemExit(0)

# 1) 换模型(直接改 embd_id，绕过 UI 维度预检)
if will_switch:
    rc.execute("UPDATE knowledgebase SET embd_id=%s WHERE id=%s", (target, kb_id))
    r.commit()
    print(f"[模型] knowledgebase.embd_id: {cur_embd} -> {target}")

# 2) 重解析全部：调 RAGFlow parse(同步置 run=1、清 progress_msg) + manager 复位 queued
base = os.environ["RAG_BASE"].rstrip("/"); key = os.environ["RAG_KEY"]
def ragflow_parse(doc_ids):
    req = urllib.request.Request(f"{base}/api/v1/datasets/{kb_id}/chunks",
        data=json.dumps({"document_ids": doc_ids}).encode(), method="POST",
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=180) as resp:
        return json.loads(resp.read() or b"{}")

items = [(d[0], d[1]) for d in docs]
ok = skip = 0
for i in range(0, len(items), batch):
    part = items[i:i + batch]; local = [c[0] for c in part]; remote = [c[1] for c in part]; bi = i // batch
    try:
        payload = ragflow_parse(remote)
        if payload.get("code") not in (0, None):
            print(f"  [跳过] 批{bi}: RAGFlow code={payload.get('code')} msg={payload.get('message')}"); skip += len(part); continue
        fmt = ",".join(["%s"] * len(local))
        mc.execute(f"UPDATE ragflow_documents SET parse_status='queued', progress=0, last_error=NULL WHERE id IN ({fmt})", local)
        m.commit(); ok += len(part)
        print(f"  [OK] 批{bi}: 重触发并复位 {len(part)} 条")
    except Exception as e:
        m.rollback(); skip += len(part); print(f"  [错误] 批{bi}: {e}")

print(f"\n完成：重触发 {ok} 条，失败/跳过 {skip} 条。后台轮询将跟踪收敛(成功→completed)。")
m.close(); r.close()
PY
