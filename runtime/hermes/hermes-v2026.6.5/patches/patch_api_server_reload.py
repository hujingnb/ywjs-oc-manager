#!/usr/bin/env python3
# patches/patch_api_server_reload.py
"""构建期补丁：给 hermes api_server 注入 POST /oc/skills/reload 端点。
在 Dockerfile RUN 阶段执行，修改镜像内
/usr/local/lib/hermes-agent/gateway/platforms/api_server.py：
  1. 在 APIServerAdapter 的 HTTP Handlers 区块前插入 _handle_oc_skills_reload 方法。
  2. 在 connect() 的路由注册块末尾追加 /oc/skills/reload 路由。

端点不需要 API_SERVER_KEY 鉴权（仅监听 127.0.0.1，只有同 pod 内的 oc-ops 能访问），
handler 直接调用 agent.skill_commands.reload_skills()，与 gateway 同进程，
调用结果即时更新 gateway 内存中的 _skill_commands 缓存。
"""

import pathlib
import sys

TARGET = pathlib.Path(
    "/usr/local/lib/hermes-agent/gateway/platforms/api_server.py"
)

# --------------------------------------------------------------------------
# 注入片段 1：_handle_oc_skills_reload 方法，插到 HTTP Handlers 区块之前。
# 不加鉴权：端点绑定在 127.0.0.1:8642，仅同 pod 内的 oc-ops 能触达，不必暴露外网。
# --------------------------------------------------------------------------
HANDLER_CODE = '''
    # ------------------------------------------------------------------
    # OC 扩展端点（oc-manager 注入）
    # ------------------------------------------------------------------

    async def _handle_oc_skills_reload(self, request: "web.Request") -> "web.Response":
        """POST /oc/skills/reload — 免重启热加载 skill 目录。

        由 oc-ops sidecar 通过 127.0.0.1:8642 触发；不需要 API_SERVER_KEY 鉴权，
        因为端点仅绑定本地回环，外部无法访问。
        handler 调用 agent.skill_commands.reload_skills()，与 gateway 同进程，
        直接更新 gateway 内存中的 _skill_commands，结果即时生效（无需重启 pod）。
        返回 JSON：{"added":[...],"removed":[...],"unchanged":[...],"total":N,"commands":N}。
        """
        from agent.skill_commands import reload_skills
        result = reload_skills()
        return web.json_response(result)

'''

# --------------------------------------------------------------------------
# 注入片段 2：路由注册行，追加到 connect() 末尾已有路由之后。
# --------------------------------------------------------------------------
ROUTE_ANCHOR = (
    '            self._app.router.add_post("/v1/runs/{run_id}/stop",'
    " self._handle_stop_run)\n"
)
ROUTE_INJECT = (
    '            # OC 扩展路由（oc-manager 注入）：供 oc-ops 触发免重启 skill 热加载\n'
    '            self._app.router.add_post("/oc/skills/reload",'
    " self._handle_oc_skills_reload)\n"
)

# --------------------------------------------------------------------------
# HTTP Handlers 区块锚点（在此之前插入新方法）
# --------------------------------------------------------------------------
HANDLER_ANCHOR = (
    "    # ------------------------------------------------------------------\n"
    "    # HTTP Handlers\n"
    "    # ------------------------------------------------------------------\n"
)


def patch(content: str) -> str:
    # 校验两个锚点都存在，任一缺失则报错中断构建
    if HANDLER_ANCHOR not in content:
        raise RuntimeError(
            "patch_api_server_reload: 找不到 HTTP Handlers 锚点——"
            "上游 api_server.py 结构变更，请更新补丁脚本。"
        )
    if ROUTE_ANCHOR not in content:
        raise RuntimeError(
            "patch_api_server_reload: 找不到路由锚点（/v1/runs/{run_id}/stop）——"
            "上游 api_server.py 结构变更，请更新补丁脚本。"
        )
    # 幂等检查：若已含注入标记则跳过，避免重复执行时内容累积
    if "_handle_oc_skills_reload" in content:
        print("patch_api_server_reload: 已注入，跳过。", flush=True)
        return content

    # 插入 handler 方法（在 HTTP Handlers 区块之前）
    content = content.replace(HANDLER_ANCHOR, HANDLER_CODE + HANDLER_ANCHOR)

    # 追加路由注册行（在最后一条路由之后）
    content = content.replace(ROUTE_ANCHOR, ROUTE_ANCHOR + ROUTE_INJECT)

    return content


if __name__ == "__main__":
    original = TARGET.read_text(encoding="utf-8")
    patched = patch(original)
    if patched is not original:
        # 内容有变更才写回，保留文件原有修改时间语义（幂等时不覆写）
        TARGET.write_text(patched, encoding="utf-8")
        print(
            "patch_api_server_reload: 成功注入 /oc/skills/reload 端点。",
            flush=True,
        )
    sys.exit(0)
