#!/usr/bin/env bash

# 该回归脚本覆盖普通 Hermes 上游 ref 的不可变性与 shell 安全边界。
set -euo pipefail

# 从脚本位置解析仓库根目录，保证从任意工作目录调用都使用同一份 Makefile 与 runtime 目录。
repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
# sentinel 使用 PID 隔离并放在临时目录，避免并发测试互相覆盖或污染仓库工作树。
sentinel="${TMPDIR:-/tmp}/oc-manager-hermes-version-guard-sentinel-$$"
# fixture 必须位于 runtime/hermes 下才能走真实 variant 派生逻辑，数组用于退出时精确清理本脚本创建的目录。
fixture_dirs=()
# 成功计数让 CI 输出明确显示本脚本实际覆盖的场景数量。
passed_cases=0

# cleanup 无论测试成功或中途失败都删除 PID 唯一 fixture 与 sentinel，禁止临时元数据残留到后续构建。
cleanup() {
  rm -rf -- "${fixture_dirs[@]}"
  rm -f -- "$sentinel"
}
trap cleanup EXIT

# create_variant 创建名称与 version.txt 严格一致的临时 variant，并通过首个参数返回 variant 名称。
create_variant() {
  local result_var=$1
  local case_name=$2
  local version="v0.0.0-guard-$$-${case_name}"
  local variant="hermes-${version}"
  local variant_dir="${repo_root}/runtime/hermes/${variant}"

  mkdir -p -- "$variant_dir"
  printf '%s\n' "$version" >"${variant_dir}/version.txt"
  fixture_dirs+=("$variant_dir")
  printf -v "$result_var" '%s' "$variant"
}

# expect_guard_pass 断言指定 variant 通过真实 Make 守卫，失败时保留 Make 原始诊断便于定位。
expect_guard_pass() {
  local variant=$1
  make --no-print-directory -s -C "$repo_root" .guard-hermes-version HERMES_VARIANT="$variant"
  passed_cases=$((passed_cases + 1))
}

# expect_guard_fail 断言守卫失败且包含预期错误分类，防止仅因无关版本命名错误而得到假阳性。
expect_guard_fail() {
  local variant=$1
  local expected_message=$2
  local output

  if output=$(make --no-print-directory -s -C "$repo_root" .guard-hermes-version HERMES_VARIANT="$variant" 2>&1); then
    printf '预期失败但守卫通过: %s\n' "$variant" >&2
    return 1
  fi
  if ! grep -Fq -- "$expected_message" <<<"$output"; then
    printf '守卫错误类型不符: %s\n%s\n' "$variant" "$output" >&2
    return 1
  fi
  passed_cases=$((passed_cases + 1))
}

# 场景一：显式四段不可变版本 tag 是上游 --branch 安装路径的合法输入。
create_variant explicit_tag explicit-tag
printf '%s\n' 'v2026.7.7.2' >"${repo_root}/runtime/hermes/${explicit_tag}/hermes-ref.txt"
expect_guard_pass "$explicit_tag"

# 场景二：历史 variant 缺少 hermes-ref.txt 时继续回退到其完整产品版本，保持旧目录可构建。
create_variant historical historical
expect_guard_pass "$historical"

# 场景三：hermes-ref.txt 已存在但为空属于元数据错误，不能静默回退到产品版本。
create_variant empty_ref empty
: >"${repo_root}/runtime/hermes/${empty_ref}/hermes-ref.txt"
expect_guard_fail "$empty_ref" 'Hermes 上游 ref 不能为空'

# 场景四：main 是浮动分支，无法保证生产镜像可复现，必须被不可变 tag 校验拒绝。
create_variant floating_ref floating
printf '%s\n' 'main' >"${repo_root}/runtime/hermes/${floating_ref}/hermes-ref.txt"
expect_guard_fail "$floating_ref" 'Hermes 上游 ref 必须是不可变版本 tag'

# 场景五：命令替换 payload 以字面量写入；守卫必须拒绝且不得执行生成 sentinel。
create_variant shell_active shell-active
printf '%s\n' "\$(touch $sentinel)" >"${repo_root}/runtime/hermes/${shell_active}/hermes-ref.txt"
expect_guard_fail "$shell_active" 'Hermes 上游 ref 包含非法字符'
test ! -e "$sentinel"

# 场景六：合法首行不能掩盖恶意第二行，整值白名单必须拒绝并保证 sentinel 不存在。
create_variant multiline multiline
printf '%s\n' 'v2026.7.7.2' "\$(touch $sentinel)" >"${repo_root}/runtime/hermes/${multiline}/hermes-ref.txt"
expect_guard_fail "$multiline" 'Hermes 上游 ref 包含非法字符'
test ! -e "$sentinel"

# 场景七：仓库当前真实 v0.18.2 元数据必须持续通过，避免临时 fixture 与实际默认版本行为脱节。
expect_guard_pass 'hermes-v0.18.2'

# 场景八：Dockerfile 使用上游 --branch 安装路径，40 位 commit SHA 也必须拒绝，不能宣称支持未实现的 --commit。
create_variant commit_sha commit-sha
printf '%s\n' 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' >"${repo_root}/runtime/hermes/${commit_sha}/hermes-ref.txt"
expect_guard_fail "$commit_sha" 'Hermes 上游 ref 必须是不可变版本 tag'

# 输出稳定的场景计数，便于本地和 CI 快速确认整个边界矩阵均已执行。
printf 'Hermes version guard: %d cases passed\n' "$passed_cases"
