# 全量验证执行计划

> 目标：跑通从静态检查到业务事务流的所有可自动化验证；遇到问题立即修复并重测，直到功能正常。
> 边界：approach B —— 静态全绿 + docker compose 启动 + 主要页面可访问 + 业务事务流（外部依赖前停止：不真实跑容器创建、不真实微信扫码）。

## 执行顺序

1. **静态检查**
   - `go test ./... -count=1`
   - `go vet ./...`
   - `gofmt -l .`（仅新文件零偏差，旧文件保持原样）
   - `web/ npm run typecheck`
   - `web/ npm run test -- --run`

2. **基础设施启动**
   - `docker compose up -d manager-postgres manager-redis`
   - 跑 migrate up（含新增的 000003 迁移）
   - `docker compose up -d manager-api manager-web`
   - `curl /healthz` 直到 200

3. **浏览器主要路由验收**（chrome-devtools MCP）
   - 登录 → 首页 / RoleAwareHome
   - Platform: 组织列表 / 充值
   - Org: 成员 / 人设 / 应用列表 / 知识库
   - 应用详情 5 tab（overview / runtime / channels / knowledge / workspace）
   - Runtime Node 列表 / 详情
   - 审计日志
   - 每个页面截图核对：无 JS 错、关键控件渲染、API 请求 200/4xx 合理

4. **业务事务流**
   - 创建组织 → 校验列表
   - 注册一个 mock runtime node（API 调用模拟 agent register）
   - 创建成员（onboarding）→ 校验 app 状态推进到 `binding_waiting`（无 docker 时 worker 应记 error，前端显示 error）
   - 删除应用 / 删除成员 → 校验 audit + 状态联动

5. **修复循环**
   - 任意步骤失败 → 修代码 → 重新跑该步骤直至通过
   - 每个修复一个 commit，commit message 注明 "fix(verify): <症状>"

## 完成标准

- [ ] 1-3 全部通过
- [ ] 4 中至少完成"创建组织 + 创建成员 + app 状态推进到 worker 处理后的稳定态"
- [ ] verification-report.md 更新最终结果
- [ ] 所有修复 commit 落盘

## 已知不在范围

- 真实容器创建（依赖真实 docker daemon 给 agent 用）
- 真实微信扫码
- Playwright E2E 自动化框架接入（C2/C3 留作后续）
