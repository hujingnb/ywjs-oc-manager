# Hermes 与 AICC 提示词隔离 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为普通 Hermes 与 AICC 提供独立、可分别演化的平台提示词及重启检测。

**Architecture:** config 负责两份常量、选择函数与 hash；Bootstrap 和 AppService 都通过 `app.AiccHidden` 使用同一选择规则。runtime renderer 保持无状态，只写入 bootstrap 提供的文本。

**Tech Stack:** Go、testify、Hermes runtime。

---

### Task 1: 定义并测试提示词选择契约

**Files:**
- Modify: `internal/config/platform_prompt.go`
- Modify: `internal/config/platform_prompt_test.go`

- [ ] 写失败测试：断言普通/AICC 常量不同、各自包含定位与完整 `skills_list` 规则；断言 `PlatformPromptForApp(false/true)` 和 `PlatformPromptHash(false/true)` 分别选择正确文本与 hash。
- [ ] 运行 `go test ./internal/config -run 'Test.*PlatformPrompt' -count=1`，确认因函数与常量尚不存在而失败。
- [ ] 最小实现两份常量、选择函数和按实例类型计算的 hash。
- [ ] 重跑目标测试并确认通过。

### Task 2: 让 bootstrap 与概览按实例类型选择

**Files:**
- Modify: `internal/service/bootstrap_service.go`
- Modify: `internal/service/bootstrap_service_test.go`
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `cmd/server/main.go`

- [ ] 写失败测试：AICC app 的 renderer 输入和 stamp 必须使用 AICC 常量/hash；普通/AICC 的 pending-restart 分别与对应 hash 比较。
- [ ] 运行 `go test ./internal/service -run 'Test.*PlatformPrompt|TestBootstrap' -count=1`，确认失败。
- [ ] `BootstrapConfig` 提供两份 prompt；`Build` 按 `app.AiccHidden` 选 prompt 并 stamp 对应 hash；server 注入两份常量；概览按类型比较 hash。
- [ ] 运行 `go test ./internal/config ./internal/service -count=1`。
