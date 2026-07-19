"""装配本地演示数据初始化流程，并提供不泄露凭据的命令行结果。"""

import os
import re
import sys

from local_seed_demo.client import (
    APIError,
    ManagerAPI,
    RequestDeadlineExceeded,
    UncertainWrite,
)
from local_seed_demo.seeder import DemoSeeder, SeedConflict, SeedRuntimeError


# 版本固定使用的两个模型厂商都必须在本地部署前配置，其他 .env 键与本脚本无关。
_VENDOR_KEYS = ("DEEPSEEK_API_KEY", "SILICONFLOW_API_KEY")

# 本地 manager 必须绕过宿主机代理；合并时保留开发者已有的内网例外项。
_LOCAL_NO_PROXY_ENTRIES = ("localhost", "127.0.0.1", ".localhost")

# 只接受 dotenv 常见的 KEY=value 赋值，避免把注释或相似前缀误判为目标 Key。
_ENV_ASSIGNMENT = re.compile(
    r"^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$"
)

# 错误边界只保留定位资源所需的文本，并清理常见认证头、模型 Key 与键值型秘密。
_BEARER_SECRET = re.compile(r"(?i)\bBearer\s+[^\s,;]+")
_MODEL_KEY_SECRET = re.compile(r"(?i)\bsk[-_][A-Za-z0-9._-]+")
_NAMED_SECRET = re.compile(
    r"(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|"
    r"public[_-]?token|widget[_-]?token|token|password|secret)"
    r"\s*[:=]\s*(?:\"[^\"]*\"|'[^']*'|[^\s,;&]+)"
)
_URL_USERINFO = re.compile(r"(?i)\b(https?://)[^/@\s]+@")
_CONTROL_CHARACTERS = re.compile(r"[\x00-\x1f\x7f]+")


class ConfigReadError(RuntimeError):
    """表示 .env 无法安全读取，异常本身不保留路径、原始字节或系统消息。"""


def _dotenv_value(raw_value):
    """提取判断非空所需的值，忽略合法引号后的注释且不保存到错误消息。"""
    value = raw_value.strip()
    if not value or value.startswith("#"):
        return ""

    if value[0] in {"'", '"'}:
        quote = value[0]
        escaped = False
        for index in range(1, len(value)):
            character = value[index]
            # 双引号允许反斜杠转义；单引号中的反斜杠按 dotenv 字面量处理。
            if quote == '"' and character == "\\" and not escaped:
                escaped = True
                continue
            if character == quote and not escaped:
                return value[1:index]
            escaped = False
        # 未闭合引号不应凭一段残缺配置绕过模型 Key 预检。
        return ""

    # 未引号值仅把空白后的 # 识别为行尾注释，保留值内部合法的 # 字符。
    comment = re.search(r"\s+#", value)
    if comment:
        value = value[: comment.start()]
    return value.strip()


def missing_vendor_keys(env_path):
    """只返回缺失的固定 Key 名，绝不向调用方暴露解析到的模型凭据。"""
    configured = {key: False for key in _VENDOR_KEYS}
    try:
        with open(env_path, encoding="utf-8") as env_file:
            for raw_line in env_file:
                line = raw_line.strip()
                if not line or line.startswith("#"):
                    continue
                match = _ENV_ASSIGNMENT.match(line)
                if not match:
                    continue
                key, raw_value = match.groups()
                if key in configured:
                    # 引号内仅有空白仍不能作为可用凭据；非空值只判断存在，不回写或输出。
                    configured[key] = bool(_dotenv_value(raw_value).strip())
    except FileNotFoundError:
        # 缺失 .env 与两个必需 Key 都未配置等价，由统一错误提示引导开发者修复。
        pass
    except (OSError, UnicodeError):
        # 权限、I/O 和编码细节可能包含本机路径或原始内容，统一转换为无上下文错误。
        raise ConfigReadError("无法读取本地 .env 配置") from None
    return [key for key in _VENDOR_KEYS if not configured[key]]


def _redact_error_text(value):
    """清理异常字段中的常见秘密与控制字符，同时保留企业、Job 等稳定目标。"""
    text = str(value)
    text = _URL_USERINFO.sub(r"\1<redacted>@", text)
    text = _BEARER_SECRET.sub("Bearer <redacted>", text)
    text = _MODEL_KEY_SECRET.sub("<redacted>", text)
    text = _NAMED_SECRET.sub(r"\1=<redacted>", text)
    # 本地固定密码虽然不用于线上，仍不允许经异常自由文本进入日志。
    text = text.replace("admin" + "123", "<redacted>")
    text = text.replace("member" + "123", "<redacted>")
    return _CONTROL_CHARACTERS.sub(" ", text).strip()


def _safe_error_message(error):
    """按异常结构生成可信诊断，API 服务端 message 永远不进入 CLI 输出。"""
    if isinstance(error, APIError):
        status = error.status if isinstance(error.status, int) else "unknown"
        return (
            "manager API 调用失败: "
            f"operation={_redact_error_text(error.operation)} "
            f"status={status} code={_redact_error_text(error.code)}"
        )
    if isinstance(error, UncertainWrite):
        return (
            f"{_redact_error_text(error.operation)} 写入结果不确定，"
            "请先重新查询稳定身份"
        )
    if isinstance(error, RequestDeadlineExceeded):
        return f"{_redact_error_text(error.operation)} 请求预算已耗尽"
    if isinstance(error, SeedRuntimeError):
        redacted = _redact_error_text(error)
        # Job last_error 与 runtime_message 是下游自由文本，只保留本地构造的目标和状态。
        job_failure = re.match(
            r"^(.+? 的 Job [^\s]+) (failed|canceled):", redacted
        )
        if job_failure:
            return f"{job_failure.group(1)} {job_failure.group(2)}（详情已隐藏）"
        runtime_failure = re.match(r"^(.+?) (初始化失败|运行时失败):", redacted)
        if runtime_failure:
            return f"{runtime_failure.group(1)} {runtime_failure.group(2)}（详情已隐藏）"
        # 超时文案完全由本地轮询器构造，可保留稳定目标；其他形态按类型安全降级。
        if re.match(r"^等待 .+ 超时（\d+s）$", redacted):
            return redacted
        return "演示资源运行失败（下游详情已隐藏）"
    # SeedConflict 只描述稳定身份和本地契约冲突，仍统一执行凭据清理。
    return _redact_error_text(error)


def _merge_no_proxy():
    """合并大小写代理例外并同步回两种环境变量，避免覆盖既有内网配置。"""
    entries = []
    for variable in ("NO_PROXY", "no_proxy"):
        for raw_entry in os.environ.get(variable, "").split(","):
            entry = raw_entry.strip()
            if entry and entry not in entries:
                entries.append(entry)
    for required in _LOCAL_NO_PROXY_ENTRIES:
        if required not in entries:
            entries.append(required)
    merged = ",".join(entries)
    os.environ["NO_PROXY"] = merged
    os.environ["no_proxy"] = merged


def main(root=None, stdout=sys.stdout, api_factory=ManagerAPI):
    """预检配置、运行严格种子流程，并以固定安全汇总返回进程退出码。"""
    if root is None:
        root = os.path.dirname(
            os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        )
    _merge_no_proxy()
    try:
        missing = missing_vendor_keys(os.path.join(root, ".env"))
    except ConfigReadError:
        print("❌ 无法读取本地 .env 配置", file=stdout)
        return 1
    if missing:
        print(
            f"❌ 本地演示数据需要模型配置，缺少: {', '.join(missing)}",
            file=stdout,
        )
        return 1

    try:
        # 平台客户端负责全局写入；企业管理员操作必须按需创建独立客户端和 token。
        platform = api_factory("http://ocm.localhost")
        platform.login("", "admin", "admin" + "123")
        DemoSeeder(
            platform,
            client_factory=lambda: api_factory("http://ocm.localhost"),
        ).run()
    except (
        APIError,
        SeedConflict,
        SeedRuntimeError,
        UncertainWrite,
        RequestDeadlineExceeded,
    ) as error:
        # 在 CLI 边界按异常结构筛选字段，不直接打印任意异常全文或服务端 message。
        print(f"❌ {_safe_error_message(error)}", file=stdout)
        return 1

    print(
        "✅ 本地演示数据就绪：2 个助手版本 / 3 个企业 / "
        "2 个普通实例 / 2 个智能客服",
        file=stdout,
    )
    return 0
