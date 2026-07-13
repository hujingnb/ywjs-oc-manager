# AICC Non-Capacity Release Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Produce fresh, reproducible evidence that every AICC release requirement other than the explicitly excluded 100-concurrent-visitor latency gate works on one kefu baseline.

**Architecture:** Treat `docs/testing/aicc-requirement-matrix.md` as the authoritative checklist and run each layer against a fixed `kefu` SHA, locally built manager images, and a freshly seeded k3d environment. Existing Go, Hermes, Vitest, Playwright, fault-recovery, and upgrade/rollback assets are reused; gaps become targeted regression tests before implementation fixes. Evidence, the matrix, and the final report must refer to the same final SHA and image tag.

**Tech Stack:** Go test, Python/Hermes tests, Vitest, Playwright Chromium, k3d, Kubernetes, Docker, MySQL, Redis, RAGFlow, new-api.

---

## File Structure

- Modify: `docs/testing/aicc-requirement-matrix.md` - final PASS/FAIL/N/A status and links to fresh evidence.
- Modify: `docs/testing/aicc-production-readiness-report.md` - final non-capacity release conclusion and residual capacity risk.
- Create: `docs/testing/evidence/aicc-non-capacity-$KEFU_SHA.log` - immutable command transcript with secrets redacted.
- Create: `docs/testing/evidence/aicc-non-capacity-browser-$KEFU_SHA.json` - Playwright and Chrome scenario result summary.
- Create: `docs/testing/evidence/aicc-non-capacity-environment-$KEFU_SHA.log` - fault recovery, GeoIP, retention, image and upgrade results.
- Modify only for discovered defects: `web/tests/e2e/aicc.spec.ts`, `web/tests/e2e/aicc-access-i18n.spec.ts`, `web/tests/e2e/aicc-knowledge.spec.ts`, and the owning Go/Hermes test and implementation.

### Task 1: Freeze the Verification Baseline

**Files:**
- Create: `docs/testing/evidence/aicc-non-capacity-$KEFU_SHA.log`
- Modify: `docs/testing/aicc-requirement-matrix.md`

- [ ] **Step 1: Record source state and preserve the user's unrelated change**

Run:
```bash
git branch --show-current
git rev-parse HEAD
git status --short
git log -1 --format='%H%n%ad%n%s' --date=iso-strict
```
Expected: branch `kefu`; record `HEAD` as `KEFU_SHA`. Do not reset or include `docs/superpowers/verifications/l4-sweep-findings.json` in any commit.

- [ ] **Step 2: Create a clean detached verification worktree**

Run:
```bash
git worktree add --detach /tmp/ocm-aicc-verify "$(git rev-parse HEAD)"
cd /tmp/ocm-aicc-verify
git status --short
```
Expected: clean detached worktree. Execute destructive environment scripts there.

- [ ] **Step 3: Reset and seed the local acceptance environment**

Run:
```bash
make local-reset
make local-status
make local-seed-e2e
```
Expected: manager-api, manager-web, MySQL, Redis, RAGFlow, new-api and ingress Ready; fixture JSON printed. Redact passwords and tokens before evidence storage.

- [ ] **Step 4: Capture image and migration baseline**

Run:
```bash
kubectl --context k3d-ocm -n ocm get deploy manager-api manager-web -o jsonpath='{range .items[*]}{.metadata.name}{" "}{range .spec.template.spec.containers[*]}{.image}{"\n"}{end}{end}'
kubectl --context k3d-ocm -n ocm exec statefulset/mysql -- sh -c 'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" -D ocm -N -e "SELECT GROUP_CONCAT(version ORDER BY version) FROM schema_migrations"'
```
Expected: images derive from `KEFU_SHA`; schema version is recorded with the SHA.

- [ ] **Step 5: Commit baseline evidence**

Run:
```bash
git add docs/testing/evidence/aicc-non-capacity-*.log
git commit -m "test(aicc): 固化非容量验证基线"
```
Expected: a path-scoped evidence commit; skip only if no durable artifact exists.

### Task 2: Execute Static, Contract, and Image Gates

**Files:**
- Create: `docs/testing/evidence/aicc-non-capacity-$KEFU_SHA.log`

- [ ] **Step 1: Run backend and Hermes tests**

Run:
```bash
go test ./... -count=1
cd runtime/hermes/hermes-v2026.7.1 && pytest -q
```
Expected: every package passes. On failure, preserve output and work on the owning regression test and implementation before continuing.

- [ ] **Step 2: Run frontend and API contract gates**

Run:
```bash
npm --prefix web run test -- --run
npm --prefix web run typecheck
npm --prefix web run build
make openapi-check
```
Expected: Vitest, `vue-tsc`, Vite, and OpenAPI synchronization pass.

- [ ] **Step 3: Verify image contents and health**

Run:
```bash
kubectl --context k3d-ocm -n ocm rollout status deploy/manager-api --timeout=300s
kubectl --context k3d-ocm -n ocm rollout status deploy/manager-web --timeout=300s
kubectl --context k3d-ocm -n ocm get pods -o wide
kubectl --context k3d-ocm -n ocm exec deploy/manager-api -- sh -c 'find / -name "*.xdb" -type f 2>/dev/null | head -20'
```
Expected: services Ready with no unexpected restarts and an XDB database in the manager image.

- [ ] **Step 4: Commit static evidence**

Run:
```bash
git add docs/testing/evidence/aicc-non-capacity-*.log
git commit -m "test(aicc): 通过非容量静态门禁"
```

### Task 3: Run Browser Business, Permission, and I18N Matrix

**Files:**
- Create: `docs/testing/evidence/aicc-non-capacity-browser-$KEFU_SHA.json`
- Test: `web/tests/e2e/aicc.spec.ts`
- Test: `web/tests/e2e/aicc-access-i18n.spec.ts`
- Test: `web/tests/e2e/aicc-knowledge.spec.ts`

- [ ] **Step 1: Run all AICC Playwright scenarios against the real local domain**

Run:
```bash
npm --prefix web run test:e2e:install
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm --prefix web run test:e2e -- aicc.spec.ts aicc-access-i18n.spec.ts aicc-knowledge.spec.ts
```
Expected: all 14 scenarios pass. Record Chromium version, count, duration, viewport, and failure artifacts.

- [ ] **Step 2: Re-run by requirement cluster**

Run:
```bash
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm --prefix web run test:e2e -- aicc.spec.ts --grep '平台开通|完整管理|限制智能体|网页挂件'
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm --prefix web run test:e2e -- aicc.spec.ts --grep '图片上传|敏感词|留资|运营策略'
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm --prefix web run test:e2e -- aicc-access-i18n.spec.ts aicc-knowledge.spec.ts
```
Expected: coverage for organization configuration, agent lifecycle, delivery/domain guard, public safety, sessions, leads, statistics, four roles, Chinese/English, all knowledge scopes, authorization revoke, and injection refusal.

- [ ] **Step 3: Audit interaction-only requirements with real Chrome DevTools**

Use Chrome DevTools MCP at `http://ocm.localhost`: platform admin opens a selected enterprise console read-only; org admin opens every workspace tab and switches agents; member receives route/API denial; public page/widget create no session until first send, restore after refresh, and create a new session only after new-chat send.

Expected: no console errors, failed requests, horizontal overflow, selected-navigation desynchronization, or visible untranslated keys. Save screenshots and redacted network summary.

- [ ] **Step 4: Add a regression test for each uncovered matrix ID**

For every matrix row not mapped to the passing scenarios, add an assertion in the owning existing spec before implementation changes.

Run:
```bash
npm --prefix web run test:e2e -- aicc.spec.ts --grep 'AICC-SESSION-07'
```
Expected: new regression fails before the fix and passes after it. Do not claim PASS from manual inspection where a stable API/UI contract can be automated.

- [ ] **Step 5: Commit browser tests, fixes, and evidence per stage**

Run:
```bash
git add web/tests/e2e internal web/src docs/testing/evidence/aicc-non-capacity-browser-*.json
git commit -m "test(aicc): 完成浏览器业务与权限回归"
```
Expected: any implementation repair is committed first as `fix(aicc): ...`.

### Task 4: Verify GeoIP, Retention, and Data Semantics

**Files:**
- Test: `internal/service/aicc_geoip_test.go`
- Test: `internal/service/aicc_retention_test.go`
- Modify: `docs/testing/evidence/aicc-non-capacity-environment-$KEFU_SHA.log`

- [ ] **Step 1: Run uncached GeoIP and retention tests**

Run:
```bash
go test ./internal/service -run 'Test.*AICC.*(GeoIP|Retention)|TestAICC(GeoIP|Retention)' -count=1 -v
go test ./internal/worker/aicc -count=1 -v
```
Expected: IPv4/IPv6, invalid lookup, valid replacement, invalid archive rejection, retention scheduler and associated cleanup tests pass.

- [ ] **Step 2: Verify runtime GeoIP resolution and update atomicity**

Run:
```bash
kubectl --context k3d-ocm -n ocm logs deploy/manager-api --since=10m | rg 'AICC|GeoIP|geoip'
kubectl --context k3d-ocm -n ocm exec deploy/manager-api -- sh -c 'find / -name "*.xdb" -type f 2>/dev/null | head -20'
```
Invoke the configured test-only update URL. Verify a valid XDB replaces the old file atomically, an invalid ZIP retains the readable old XDB, no GitHub URL is used, and a known test IP yields a non-`未知` region.

- [ ] **Step 3: Verify retention against disposable records**

Create expired session, linked lead, and uploaded image through the service fixture or scoped SQL fixture; execute the worker once and query their generated IDs.

Run:
```bash
go test ./internal/service -run TestAICCRetention -count=1 -v
```
Expected: expired records/files are removed; non-expired records remain. Redact generated identifiers in committed evidence.

- [ ] **Step 4: Commit data-environment evidence**

Run:
```bash
git add internal/service internal/worker/aicc docs/testing/evidence/aicc-non-capacity-environment-*.log
git commit -m "test(aicc): 验证地域与留存任务"
```

### Task 5: Execute Dependency Fault Recovery and Core Smoke

**Files:**
- Test: `scripts/aicc-readiness/fault-recovery_test.sh`
- Modify: `docs/testing/evidence/aicc-non-capacity-environment-$KEFU_SHA.log`

- [ ] **Step 1: Verify fault script guards**

Run:
```bash
bash scripts/aicc-readiness/fault-recovery_test.sh
AICC_PUBLIC_TOKEN="$AICC_PUBLIC_TOKEN" bash scripts/aicc-readiness/fault-recovery.sh --dry-run
```
Expected: script tests pass; dry-run lists only `k3d-ocm`, `ocm`, and `oc-apps` operations.

- [ ] **Step 2: Run live dependency failure and recovery drill**

Run:
```bash
AICC_PUBLIC_TOKEN="$AICC_PUBLIC_TOKEN" bash scripts/aicc-readiness/fault-recovery.sh | tee /tmp/aicc-fault-recovery.log
```
Expected: Hermes, RAGFlow, new-api, Redis, MySQL, and manager-api fail safely while unavailable; after recovery, the same conversation continues with exactly one user and one assistant message per retry key.

- [ ] **Step 3: Re-run browser smoke after recovery**

Run:
```bash
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm --prefix web run test:e2e -- aicc.spec.ts aicc-knowledge.spec.ts
```
Expected: public chat, knowledge retrieval, sessions, and leads work after all dependencies recover.

- [ ] **Step 4: Commit fault evidence**

Run:
```bash
git add scripts/aicc-readiness docs/testing/evidence/aicc-non-capacity-environment-*.log
git commit -m "test(aicc): 完成依赖故障恢复验证"
```

### Task 6: Rehearse Upgrade, Rollback, and Final Browser Smoke

**Files:**
- Test: `scripts/aicc-readiness/upgrade-rollback_test.sh`
- Modify: `docs/testing/evidence/aicc-non-capacity-environment-$KEFU_SHA.log`

- [ ] **Step 1: Run upgrade script tests and guards**

Run:
```bash
bash scripts/aicc-readiness/upgrade-rollback_test.sh
git status --short
```
Expected: tests pass. Run the live drill from the clean detached worktree because it rejects a dirty worktree.

- [ ] **Step 2: Run the live upgrade and controlled rollback drill**

Run:
```bash
AICC_READINESS_RUN_BROWSER_SMOKE=1 bash scripts/aicc-readiness/upgrade-rollback.sh | tee /tmp/aicc-upgrade-rollback.log
```
Expected: master history upgrades without loss; old images fail only at documented schema boundary; baseline restoration works; final kefu images/migrations and browser smoke recover.

- [ ] **Step 3: Verify final deployment and all browser scenarios**

Run:
```bash
kubectl --context k3d-ocm -n ocm get deploy manager-api manager-web -o jsonpath='{range .items[*]}{.metadata.name}{" "}{range .spec.template.spec.containers[*]}{.image}{"\n"}{end}{end}'
PLAYWRIGHT_BASE_URL=http://ocm.localhost npm --prefix web run test:e2e -- aicc.spec.ts aicc-access-i18n.spec.ts aicc-knowledge.spec.ts
```
Expected: image tag matches final SHA and the full suite passes after migration/recovery.

- [ ] **Step 4: Commit upgrade evidence**

Run:
```bash
git add scripts/aicc-readiness docs/testing/evidence/aicc-non-capacity-environment-*.log
git commit -m "test(aicc): 完成升级回退演练"
```

### Task 7: Reconcile Matrix and Publish the Restricted Decision

**Files:**
- Modify: `docs/testing/aicc-requirement-matrix.md`
- Modify: `docs/testing/aicc-production-readiness-report.md`
- Modify: `docs/testing/evidence/aicc-non-capacity-$KEFU_SHA.log`

- [ ] **Step 1: Reconcile every matrix row with fresh evidence**

For every ID except `AICC-LOAD-01`, replace `BLOCKED` with `PASS` or `FAIL` and cite the exact test, browser artifact, fault log, or upgrade log. Mark `AICC-LOAD-01` `N/A` only for this scope and retain: 25,584/25,584 success, zero session mismatch, P95 24,502ms above the 15,000ms production threshold.

Expected: no non-capacity `BLOCKED` row or uncited conclusion.

- [ ] **Step 2: Write final report and constrained conclusion**

Include final SHA/images, commands/counts, four identities/viewports, dependency results, GeoIP/retention, upgrade/rollback, fixed defect commits, and capacity risk.

Expected when all non-capacity items pass: `非容量功能门禁通过；容量门禁未通过，不能作为完整生产 GO`. Any non-capacity failure is `NO-GO`.

- [ ] **Step 3: Perform hygiene validation**

Run:
```bash
rg -n 'TODO|TBD|待定|BLOCKED' docs/testing/aicc-requirement-matrix.md docs/testing/aicc-production-readiness-report.md
git diff --check
git status --short
```
Expected: no unqualified `BLOCKED`, no placeholders/whitespace errors, and no change to the user's unrelated verification JSON file.

- [ ] **Step 4: Commit the final evidence and decision**

Run:
```bash
git add docs/testing/aicc-requirement-matrix.md docs/testing/aicc-production-readiness-report.md docs/testing/evidence/aicc-non-capacity-*
git commit -m "docs(aicc): 更新非容量发布验证结论"
```
Expected: one final path-scoped documentation commit whose SHA is reported with the final decision.
