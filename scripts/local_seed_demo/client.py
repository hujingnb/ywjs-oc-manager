"""提供本地演示数据脚本访问 manager API 所需的最小安全客户端。"""

import http.client
import json
import queue
import threading
import time
import urllib.error
import urllib.request


# 这些状态通常不代表请求语义永久失败，但仅允许幂等读取自动重放。
TRANSIENT_STATUSES = {429, 502, 503, 504}

# 有限退避避免本地服务恢复期间形成紧密重试循环，也为调用方保留确定的等待上限。
_GET_RETRY_DELAYS = (1, 2, 4, 8, 16)


class APIError(RuntimeError):
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


class UncertainWrite(RuntimeError):
    """表示写请求可能已生效但没有响应，调用方必须先按稳定身份重新查询。"""

    def __init__(self, operation):
        """异常只标识安全操作名，不保留可能含令牌或业务数据的请求上下文。"""
        self.operation = operation
        super().__init__(f"{operation} 写入结果不确定，请先重新查询稳定身份")


class RequestDeadlineExceeded(RuntimeError):
    """表示只读请求的调用方绝对 deadline 已耗尽，不包含 URL 参数或凭据。"""

    def __init__(self, operation):
        """异常仅保留由调用方提供的安全操作名，且不得进入瞬时错误重试。"""
        self.operation = operation
        super().__init__(f"{operation} 请求预算已耗尽")


class _InvalidResponse(RuntimeError):
    """表示响应消息边界或编码不符合 JSON API 契约，仅供客户端内部安全映射。"""


class ManagerAPI:
    """封装演示数据脚本需要的认证、读取和写入 HTTP 操作。"""

    def __init__(
        self,
        base_url,
        sleep=time.sleep,
        timeout=60,
        monotonic=time.monotonic,
    ):
        """注入休眠和单调时钟便于测试，并将访问令牌限制在客户端内存中。"""
        self.base_url = base_url.rstrip("/")
        self.sleep = sleep
        self.timeout = timeout
        self.monotonic = monotonic
        self.access_token = None

    def wait_ready(self, timeout=60):
        """经登录使用的同一 Ingress 等待 manager 健康入口在总预算内就绪。"""
        operation = "GET /healthz"
        # 外层 deadline 还覆盖 DNS；内层继续约束 socket、响应读取与有限退避。
        deadline = self.monotonic() + timeout
        remaining = deadline - self.monotonic()
        if remaining <= 0:
            raise RequestDeadlineExceeded(operation)

        result_queue = queue.Queue(maxsize=1)

        def request_health():
            """在 daemon worker 中仅执行匿名幂等 GET，并回传结果或普通异常。"""
            try:
                payload = self._request(
                    "GET",
                    "/healthz",
                    None,
                    authenticated=False,
                    deadline=deadline,
                )
            except Exception as error:
                # 不捕获 KeyboardInterrupt/SystemExit；普通异常由主线程保持类型重新抛出。
                result_queue.put_nowait((False, error))
                return
            result_queue.put_nowait((True, payload))

        worker = threading.Thread(
            target=request_health,
            name="manager-health-check",
            daemon=True,
        )
        worker.start()
        remaining = deadline - self.monotonic()
        if remaining <= 0:
            raise RequestDeadlineExceeded(operation)
        try:
            succeeded, result = result_queue.get(timeout=remaining)
        except queue.Empty:
            # daemon 可能稍后自行结束，但其匿名 GET 不会修改 token 或主线程输出。
            raise RequestDeadlineExceeded(operation) from None
        if not succeeded:
            raise result
        payload = result
        # 只有明确的 ok 对象才可进入登录；模糊健康状态不得触发不可重放的 POST。
        if not isinstance(payload, dict) or payload.get("status") != "ok":
            raise APIError(
                operation,
                200,
                "invalid_health_response",
                "manager 健康检查响应异常",
            )
        return payload

    def login(self, org_code, username, password):
        """以明文 JSON 完成受控本地登录，并只保留返回的访问令牌与用户数据。"""
        data = self.post(
            "/api/v1/auth/login",
            {"org_code": org_code, "username": username, "password": password},
            authenticated=False,
        )
        self.access_token = data["tokens"]["access_token"]
        return data["user"]

    def get(self, path, deadline=None):
        """执行认证读取；可选绝对 deadline 同时限制请求、退避和后续重试。"""
        return self._request(
            "GET", path, None, authenticated=True, deadline=deadline
        )

    def post(self, path, body, authenticated=True):
        """执行不可盲目重放的写入，连接中断时向调用方暴露不确定结果。"""
        return self._request(
            "POST", path, body, authenticated=authenticated, deadline=None
        )

    def patch(self, path, body):
        """执行认证更新，并与 POST 一样禁止在连接中断后自动重放。"""
        return self._request(
            "PATCH", path, body, authenticated=True, deadline=None
        )

    def _request(self, method, path, body, authenticated, deadline=None):
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
                request_timeout = self._request_timeout(deadline, operation)
                with urllib.request.urlopen(request, timeout=request_timeout) as response:
                    payload = self._read_json_response(
                        response,
                        deadline,
                        operation,
                    )
                    # manager handler 直接返回顶层业务对象，不额外包装 data envelope。
                    return payload
            except urllib.error.HTTPError as error:
                # 仅 GET 的指定瞬时状态可重试，避免 POST/PATCH 被服务端重复执行。
                should_retry = (
                    method == "GET"
                    and error.code in TRANSIENT_STATUSES
                    and attempt < len(retry_delays)
                )
                terminal_error = None
                try:
                    if not should_retry:
                        terminal_error = self._api_error(
                            operation,
                            error,
                            deadline,
                        )
                finally:
                    # 所有 HTTPError 均统一关闭，终态错误也不得泄漏响应连接。
                    error.close()

                if should_retry:
                    self._sleep_before_retry(
                        retry_delays[attempt], deadline, operation
                    )
                    continue
                raise terminal_error from None
            except (urllib.error.URLError, OSError, http.client.HTTPException):
                # 连接异常同样只允许 GET 退避；写入无法判断服务端是否已经提交。
                if method == "GET" and attempt < len(retry_delays):
                    self._sleep_before_retry(
                        retry_delays[attempt], deadline, operation
                    )
                    continue
                if method in {"POST", "PATCH"}:
                    raise UncertainWrite(operation) from None
                raise APIError(operation, None, "connection_error", "连接 manager 失败") from None

        # 循环边界保证所有路径均已返回或抛错，此处仅用于静态表达不可达状态。
        raise RuntimeError("不可达的请求状态")

    def _request_timeout(self, deadline, operation):
        """计算本次 urlopen 的正 timeout，deadline 耗尽时禁止发出网络请求。"""
        if deadline is None:
            return self.timeout
        remaining = deadline - self.monotonic()
        if remaining <= 0:
            raise RequestDeadlineExceeded(operation)
        timeout = min(self.timeout, remaining)
        if timeout <= 0:
            raise RequestDeadlineExceeded(operation)
        return timeout

    def _read_json_response(self, response, deadline, operation):
        """在绝对预算内读取成功响应，并将非法 JSON 统一映射为安全 APIError。"""
        status = self._response_status(response)
        try:
            response_body = self._read_response_body(
                response,
                deadline,
                operation,
            )
            return json.loads(response_body)
        except (
            _InvalidResponse,
            json.JSONDecodeError,
            UnicodeDecodeError,
            http.client.IncompleteRead,
        ):
            raise APIError(
                operation,
                status,
                "invalid_response",
                "manager 响应格式异常",
            ) from None

    def _read_response_body(self, response, deadline, operation):
        """分段读取响应体，并在每次阻塞读取前后执行绝对 deadline 校验。"""
        chunks = []
        expected_length = self._content_length(response)
        read1 = getattr(response, "read1", None)
        while True:
            self._raise_if_deadline_exhausted(deadline, operation)
            self._limit_response_read_timeout(response, deadline, operation)
            # HTTPResponse.read1 只取当前可用字节；普通 file-like 退化为单字节读取。
            chunk = read1(64 * 1024) if callable(read1) else response.read(1)
            self._raise_if_deadline_exhausted(deadline, operation)
            if not chunk:
                response_body = b"".join(chunks)
                if (
                    expected_length is not None
                    and len(response_body) != expected_length
                ):
                    raise _InvalidResponse("Content-Length 与响应体长度不一致")
                return response_body
            chunks.append(chunk)

    @staticmethod
    def _content_length(response):
        """严格解析重复 Content-Length，并拒绝非法、负数或互相冲突的声明。"""
        headers = getattr(response, "headers", None)
        get_all = getattr(headers, "get_all", None)
        if not callable(get_all):
            return None
        values = get_all("Content-Length") or []
        if not values:
            return None

        parsed_lengths = []
        for value in values:
            if (
                not isinstance(value, str)
                or not value.isascii()
                or not value.isdigit()
            ):
                raise _InvalidResponse("Content-Length 不是非负十进制整数")
            parsed_lengths.append(int(value))
        if any(length != parsed_lengths[0] for length in parsed_lengths[1:]):
            raise _InvalidResponse("多个 Content-Length 声明互相冲突")
        return parsed_lengths[0]

    def _limit_response_read_timeout(self, response, deadline, operation):
        """兼容性 best-effort 收紧 CPython HTTPResponse socket 的单次读取预算。"""
        if deadline is None:
            return
        timeout = self._request_timeout(deadline, operation)
        # urllib 未公开底层 socket；仅识别 CPython 当前链路，健康总上限不依赖此私有结构。
        response_fp = getattr(response, "fp", None)
        raw_stream = getattr(response_fp, "raw", None)
        response_socket = getattr(raw_stream, "_sock", None)
        if response_socket is not None:
            response_socket.settimeout(timeout)

    @staticmethod
    def _response_status(response):
        """兼容 HTTPResponse 与测试 file-like，提取可安全写入 APIError 的状态码。"""
        status = getattr(response, "status", None)
        getcode = getattr(response, "getcode", None)
        if status is None and callable(getcode):
            status = getcode()
        return status

    def _sleep_before_retry(self, delay, deadline, operation):
        """将 GET 退避截断到剩余预算，并在睡后阻止 deadline 外的新请求。"""
        if deadline is None:
            self.sleep(delay)
            return
        remaining = deadline - self.monotonic()
        if remaining <= 0:
            raise RequestDeadlineExceeded(operation)
        sleep_seconds = min(delay, remaining)
        if sleep_seconds <= 0:
            raise RequestDeadlineExceeded(operation)
        self.sleep(sleep_seconds)
        self._raise_if_deadline_exhausted(deadline, operation)

    def _raise_if_deadline_exhausted(self, deadline, operation):
        """在响应读取或退避完成后复查绝对预算，避免接受 deadline 外的结果。"""
        if deadline is not None and self.monotonic() >= deadline:
            raise RequestDeadlineExceeded(operation)

    def _api_error(self, operation, error, deadline):
        """从 HTTP 错误响应中只提取 code/message，丢弃可能含敏感信息的其余字段。"""
        try:
            response_body = self._read_response_body(
                error,
                deadline,
                operation,
            )
            payload = json.loads(response_body)
        except (
            _InvalidResponse,
            json.JSONDecodeError,
            UnicodeDecodeError,
            http.client.IncompleteRead,
            http.client.HTTPException,
            OSError,
        ):
            payload = {}
        # JSON null、数组或标量虽语法合法，却不具备错误对象字段，统一安全降级。
        if not isinstance(payload, dict):
            payload = {}
        code = payload.get("code", "http_error")
        message = payload.get("message", "manager 请求失败")
        return APIError(operation, error.code, code, message)
