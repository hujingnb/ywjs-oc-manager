"""提供本地演示数据脚本访问 manager API 所需的最小安全客户端。"""

import http.client
import json
import time
import urllib.error
import urllib.request


# 这些状态通常不代表请求语义永久失败，但仅允许幂等读取自动重放。
TRANSIENT_STATUSES = {429, 502, 503, 504}

# 有限退避避免本地服务恢复期间形成紧密重试循环，也为调用方保留确定的等待上限。
_GET_RETRY_DELAYS = (1, 2, 4, 8, 16)


class APIError(Exception):
    """表示 manager 返回的安全 API 错误，不保存请求凭据或原始响应体。"""

    def __init__(self, operation, status, code, message):
        """仅将操作、状态和服务端安全字段纳入异常文本，避免请求内容意外外泄。"""
        self.operation = operation
        self.status = status
        self.code = code
        self.safe_message = message
        super().__init__(
            f"operation={operation} status={status} code={code} message={message}"
        )


class UncertainWrite(Exception):
    """表示写请求可能已生效但没有响应，调用方必须先按稳定身份重新查询。"""

    def __init__(self, operation):
        """异常只标识安全操作名，不保留可能含令牌或业务数据的请求上下文。"""
        self.operation = operation
        super().__init__(f"{operation} 写入结果不确定，请先重新查询稳定身份")


class ManagerAPI:
    """封装演示数据脚本需要的认证、读取和写入 HTTP 操作。"""

    def __init__(self, base_url, sleep=time.sleep, timeout=60):
        """注入休眠函数便于测试退避，同时将访问令牌限制在客户端内存中。"""
        self.base_url = base_url.rstrip("/")
        self.sleep = sleep
        self.timeout = timeout
        self.access_token = None

    def login(self, org_code, username, password):
        """以明文 JSON 完成受控本地登录，并只保留返回的访问令牌与用户数据。"""
        data = self.post(
            "/api/v1/auth/login",
            {"org_code": org_code, "username": username, "password": password},
            authenticated=False,
        )
        self.access_token = data["tokens"]["access_token"]
        return data["user"]

    def get(self, path):
        """执行可安全重放的认证读取；瞬时故障由底层按有限退避处理。"""
        return self._request("GET", path, None, authenticated=True)

    def post(self, path, body, authenticated=True):
        """执行不可盲目重放的写入，连接中断时向调用方暴露不确定结果。"""
        return self._request("POST", path, body, authenticated=authenticated)

    def patch(self, path, body):
        """执行认证更新，并与 POST 一样禁止在连接中断后自动重放。"""
        return self._request("PATCH", path, body, authenticated=True)

    def _request(self, method, path, body, authenticated):
        """发送 JSON 请求，并严格区分可重试读取与可能已生效的写入。"""
        operation = f"{method} {path}"
        headers = {"Accept": "application/json"}
        encoded_body = None

        # 只有确实存在内存令牌时才设置认证头，登录请求不会携带陈旧或空凭据。
        if authenticated and self.access_token:
            headers["Authorization"] = f"Bearer {self.access_token}"

        # GET 不发送实体；写请求统一 JSON 编码并显式声明媒体类型。
        if body is not None:
            encoded_body = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"

        # GET 最多经历五档退避；写请求始终只有一次网络尝试。
        retry_delays = _GET_RETRY_DELAYS if method == "GET" else ()
        for attempt in range(len(retry_delays) + 1):
            request = urllib.request.Request(
                f"{self.base_url}{path}",
                data=encoded_body,
                headers=headers,
                method=method,
            )
            try:
                with urllib.request.urlopen(request, timeout=self.timeout) as response:
                    payload = json.load(response)
                    return payload["data"]
            except urllib.error.HTTPError as error:
                # 仅 GET 的指定瞬时状态可重试，避免 POST/PATCH 被服务端重复执行。
                if method == "GET" and error.code in TRANSIENT_STATUSES and attempt < len(retry_delays):
                    self.sleep(retry_delays[attempt])
                    continue
                raise self._api_error(operation, error) from None
            except (urllib.error.URLError, OSError, http.client.HTTPException):
                # 连接异常同样只允许 GET 退避；写入无法判断服务端是否已经提交。
                if method == "GET" and attempt < len(retry_delays):
                    self.sleep(retry_delays[attempt])
                    continue
                if method in {"POST", "PATCH"}:
                    raise UncertainWrite(operation) from None
                raise APIError(operation, None, "connection_error", "连接 manager 失败") from None

        # 循环边界保证所有路径均已返回或抛错，此处仅用于静态表达不可达状态。
        raise RuntimeError("不可达的请求状态")

    @staticmethod
    def _api_error(operation, error):
        """从 HTTP 错误响应中只提取 code/message，丢弃可能含敏感信息的其余字段。"""
        try:
            payload = json.load(error)
        except (json.JSONDecodeError, UnicodeDecodeError):
            payload = {}
        code = payload.get("code", "http_error")
        message = payload.get("message", "manager 请求失败")
        return APIError(operation, error.code, code, message)
