"""验证本地演示数据 CLI 的配置预检、依赖装配和安全退出行为。"""

import io
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

from local_seed_demo.client import APIError, RequestDeadlineExceeded, UncertainWrite
from local_seed_demo.cli import main, missing_vendor_keys
from local_seed_demo.seeder import SeedConflict, SeedRuntimeError


class FakeAPI:
    """记录登录调用，避免 CLI 测试访问真实 manager。"""

    def __init__(self, base_url):
        """保存公开地址及后续登录事实，不保存测试密码到异常消息。"""
        self.base_url = base_url
        self.logins = []

    def login(self, org_code, username, password):
        """记录依赖装配是否使用平台管理员约定。"""
        self.logins.append((org_code, username, password))


class RecordingFactory:
    """为每次调用创建独立客户端，并记录 ManagerAPI 构造参数。"""

    def __init__(self):
        """初始化客户端调用记录。"""
        self.clients = []

    def __call__(self, base_url):
        """按真实 ManagerAPI 的单个必需位置参数签名创建客户端。"""
        client = FakeAPI(base_url)
        self.clients.append(client)
        return client


class LocalSeedDemoCLITest(unittest.TestCase):
    """覆盖 CLI 在宿主机启动前可验证的全部分支。"""

    def setUp(self):
        """为每个场景创建隔离仓库根目录和 .env。"""
        self.tempdir = tempfile.TemporaryDirectory()
        self.root = self.tempdir.name

    def tearDown(self):
        """回收测试创建的临时配置目录。"""
        self.tempdir.cleanup()

    def write_env(self, content):
        """写入仅供当前场景解析的本地模型配置。"""
        path = os.path.join(self.root, ".env")
        with open(path, "w", encoding="utf-8") as env_file:
            env_file.write(content)
        return path

    # 覆盖 .env 不存在时两个固定模型 Key 都会报告缺失的预检路径。
    def test_missing_env_reports_both_vendor_keys(self):
        self.assertEqual(
            ["DEEPSEEK_API_KEY", "SILICONFLOW_API_KEY"],
            missing_vendor_keys(os.path.join(self.root, ".env")),
        )

    # 覆盖缺少任一厂商 Key 都失败且不会把另一 Key 的值输出到日志。
    def test_missing_each_vendor_key_returns_safe_failure(self):
        cases = (
            # 只配置 DeepSeek，必须明确报告 SiliconFlow 缺失。
            ("DEEPSEEK_API_KEY=secret-deepseek\n", "SILICONFLOW_API_KEY", "secret-deepseek"),
            # 只配置 SiliconFlow，必须明确报告 DeepSeek 缺失。
            ("SILICONFLOW_API_KEY=secret-silicon\n", "DEEPSEEK_API_KEY", "secret-silicon"),
        )
        for content, missing_key, secret in cases:
            with self.subTest(missing_key=missing_key):
                self.write_env(content)
                output = io.StringIO()
                factory = RecordingFactory()
                self.assertEqual(1, main(root=self.root, stdout=output, api_factory=factory))
                self.assertIn(missing_key, output.getvalue())
                self.assertNotIn(secret, output.getvalue())
                self.assertEqual([], factory.clients)

    # 覆盖空值、引号、export 前缀和行尾注释的 .env 兼容语义。
    def test_vendor_key_parser_handles_empty_quotes_export_and_comments(self):
        cases = (
            # 无引号空值即使带注释也视为缺失。
            ("DEEPSEEK_API_KEY= # empty\nSILICONFLOW_API_KEY=x\n", ["DEEPSEEK_API_KEY"]),
            # 空引号仍是空值，不能绕过启动预检。
            ("DEEPSEEK_API_KEY='' # empty\nSILICONFLOW_API_KEY=\"\"\n", ["DEEPSEEK_API_KEY", "SILICONFLOW_API_KEY"]),
            # 引号中只有空白同样不是可用模型凭据，必须按空值处理。
            ("export DEEPSEEK_API_KEY='   '\nSILICONFLOW_API_KEY=\"  \"\n", ["DEEPSEEK_API_KEY", "SILICONFLOW_API_KEY"]),
            # export、单双引号以及引号后的注释均应正确提取非空事实。
            ("export DEEPSEEK_API_KEY='deep#value' # note\nexport SILICONFLOW_API_KEY=\"silicon value\" # note\n", []),
            # 未引号值的空白后行尾注释不属于 Key 值。
            ("DEEPSEEK_API_KEY=deep # note\nSILICONFLOW_API_KEY=silicon # note\n", []),
        )
        for content, expected in cases:
            with self.subTest(expected=expected):
                path = self.write_env("\n# model keys\n" + content)
                self.assertEqual(expected, missing_vendor_keys(path))

    # 覆盖成功时平台登录、企业客户端隔离和固定资源数量汇总。
    def test_complete_run_logs_in_and_uses_independent_clients(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        output = io.StringIO()
        factory = RecordingFactory()
        observed = {}

        class FakeSeeder:
            """在不运行真实编排的前提下检查 CLI 注入的两类客户端。"""

            def __init__(self, platform, client_factory):
                observed["platform"] = platform
                observed["org_clients"] = [client_factory(), client_factory()]

            def run(self):
                """模拟编排成功，CLI 不依赖返回对象统计资源数量。"""
                return object()

        with mock.patch("local_seed_demo.cli.DemoSeeder", FakeSeeder):
            self.assertEqual(0, main(root=self.root, stdout=output, api_factory=factory))

        self.assertEqual(3, len(factory.clients))
        self.assertIs(factory.clients[0], observed["platform"])
        self.assertIsNot(observed["org_clients"][0], observed["org_clients"][1])
        self.assertEqual([("", "admin", "admin123")], factory.clients[0].logins)
        self.assertTrue(all(client.base_url == "http://ocm.localhost" for client in factory.clients))
        self.assertEqual(
            "✅ 本地演示数据就绪：2 个助手版本 / 3 个企业 / 2 个普通实例 / 2 个智能客服\n",
            output.getvalue(),
        )
        self.assertNotIn("token", output.getvalue().lower())

    # 覆盖上下大小写 NO_PROXY 既有条目合并且本地域名不重复丢失的路径。
    def test_no_proxy_merges_existing_entries_and_syncs_both_names(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        factory = RecordingFactory()
        with mock.patch.dict(
            os.environ,
            {"NO_PROXY": "service.internal,localhost", "no_proxy": "other.internal,127.0.0.1"},
            clear=False,
        ), mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
            seeder.return_value.run.return_value = object()
            self.assertEqual(0, main(root=self.root, stdout=io.StringIO(), api_factory=factory))
            expected = "service.internal,localhost,other.internal,127.0.0.1,.localhost"
            self.assertEqual(expected, os.environ["NO_PROXY"])
            self.assertEqual(expected, os.environ["no_proxy"])

    # 覆盖所有预期运行时异常被转换为单行脱敏失败消息且退出码为 1。
    def test_expected_runtime_errors_return_single_line_failure(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        errors = (
            # manager 明确拒绝请求时输出其安全错误字段。
            APIError("GET /safe", 409, "conflict", "资源冲突"),
            # 稳定身份冲突时提示人工修正资源。
            SeedConflict("demo-full 资源冲突"),
            # 异步运行时失败时保留可定位的安全对象信息。
            SeedRuntimeError("演示助手启动失败"),
            # 写响应丢失时要求重新查询，不能打印请求数据。
            UncertainWrite("POST /api/v1/organizations"),
            # 只读 deadline 用尽时也应以预期错误退出。
            RequestDeadlineExceeded("GET /api/v1/jobs/job-safe"),
        )
        for error in errors:
            with self.subTest(error_type=type(error).__name__):
                output = io.StringIO()
                factory = RecordingFactory()
                with mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
                    seeder.return_value.run.side_effect = error
                    self.assertEqual(1, main(root=self.root, stdout=output, api_factory=factory))
                self.assertEqual(1, len(output.getvalue().splitlines()))
                self.assertTrue(output.getvalue().startswith("❌ "))
                self.assertNotIn("Traceback", output.getvalue())
                self.assertNotIn("admin123", output.getvalue())

    # 覆盖服务端消息和运行时下游错误即使夹带多种凭据也只输出脱敏后的稳定诊断。
    def test_runtime_errors_redact_untrusted_secret_text(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        errors = (
            # APIError 的服务端 message 完全不可信，只保留结构化操作、状态和错误码。
            APIError(
                "POST /api/v1/apps?token=query-secret",
                500,
                "internal_error",
                "Bearer bearer-secret sk-api-message password=message-pass",
            ),
            # Job/runtime 的自由文本需保留稳定目标，但必须清理常见 token、密码和 URL 凭据。
            SeedRuntimeError(
                "企业 demo-full 的 Job job-1 failed: Bearer bearer-runtime "
                "sk-runtime-key password=runtime-pass "
                "https://url-user:url-pass@example.local/run?token=url-query"
            ),
            # SeedConflict 同样经过统一清理，同时保持普通业务冲突原文可定位。
            SeedConflict("demo-full 资源冲突 access_token=access-secret"),
        )
        secrets = (
            "query-secret",
            "bearer-secret",
            "sk-api-message",
            "message-pass",
            "bearer-runtime",
            "sk-runtime-key",
            "runtime-pass",
            "url-user",
            "url-pass",
            "url-query",
            "access-secret",
        )
        for error in errors:
            with self.subTest(error_type=type(error).__name__):
                output = io.StringIO()
                with mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
                    seeder.return_value.run.side_effect = error
                    self.assertEqual(
                        1,
                        main(
                            root=self.root,
                            stdout=output,
                            api_factory=RecordingFactory(),
                        ),
                    )
                self.assertEqual(1, len(output.getvalue().splitlines()))
                for secret in secrets:
                    self.assertNotIn(secret, output.getvalue())
                if isinstance(error, SeedRuntimeError):
                    self.assertIn("企业 demo-full 的 Job job-1 failed", output.getvalue())

    # 覆盖无法识别结构的下游自由文本不进入日志，按异常类型降级为通用说明。
    def test_unstructured_runtime_error_hides_entire_downstream_message(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        output = io.StringIO()
        with mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
            seeder.return_value.run.side_effect = SeedRuntimeError(
                "opaque downstream credential unexpected-private-value"
            )
            self.assertEqual(
                1,
                main(root=self.root, stdout=output, api_factory=RecordingFactory()),
            )
        self.assertEqual("❌ 演示资源运行失败（下游详情已隐藏）\n", output.getvalue())
        self.assertNotIn("unexpected-private-value", output.getvalue())

    # 覆盖普通安全冲突文案无需变化即可保留企业定位信息。
    def test_safe_seed_conflict_message_is_preserved(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        output = io.StringIO()
        with mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
            seeder.return_value.run.side_effect = SeedConflict("demo-full 资源冲突")
            self.assertEqual(
                1,
                main(root=self.root, stdout=output, api_factory=RecordingFactory()),
            )
        self.assertEqual("❌ demo-full 资源冲突\n", output.getvalue())

    # 覆盖权限和编码读取失败都转换为固定单行配置错误，且不泄露路径或原始字节内容。
    def test_env_read_errors_return_safe_configuration_failure(self):
        errors = (
            # .env 权限不足不得把宿主机绝对路径带到日志。
            PermissionError("/private/project/.env permission-secret"),
            # 非 UTF-8 内容不得打印解码异常中的原始字节和原因。
            UnicodeDecodeError("utf-8", b"\xff", 0, 1, "decode-secret"),
        )
        for error in errors:
            with self.subTest(error_type=type(error).__name__):
                output = io.StringIO()
                with mock.patch("builtins.open", side_effect=error):
                    self.assertEqual(
                        1,
                        main(
                            root=self.root,
                            stdout=output,
                            api_factory=RecordingFactory(),
                        ),
                    )
                self.assertEqual("❌ 无法读取本地 .env 配置\n", output.getvalue())
                self.assertNotIn("secret", output.getvalue())

    # 覆盖真实 Python 子进程能够导入薄入口，并在隔离根目录缺 Key 时安全退出而不访问网络。
    def test_executable_entrypoint_smoke_with_missing_keys(self):
        isolated_root = os.path.join(self.root, "isolated")
        isolated_scripts = os.path.join(isolated_root, "scripts")
        os.makedirs(isolated_scripts)
        repository_scripts = os.path.dirname(os.path.dirname(__file__))
        shutil.copy2(
            os.path.join(repository_scripts, "local-seed-demo.py"),
            isolated_scripts,
        )
        shutil.copytree(
            os.path.join(repository_scripts, "local_seed_demo"),
            os.path.join(isolated_scripts, "local_seed_demo"),
        )

        result = subprocess.run(
            [sys.executable, "scripts/local-seed-demo.py"],
            cwd=isolated_root,
            capture_output=True,
            text=True,
            check=False,
        )

        self.assertEqual(1, result.returncode)
        self.assertIn("DEEPSEEK_API_KEY", result.stdout)
        self.assertIn("SILICONFLOW_API_KEY", result.stdout)
        self.assertNotIn("Traceback", result.stderr)

    # 覆盖 KeyboardInterrupt 不被普通失败处理吞掉，允许终端按约定中断进程。
    def test_keyboard_interrupt_propagates(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        factory = RecordingFactory()
        with mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
            seeder.return_value.run.side_effect = KeyboardInterrupt()
            with self.assertRaises(KeyboardInterrupt):
                main(root=self.root, stdout=io.StringIO(), api_factory=factory)

    # 覆盖 SystemExit 不被 RuntimeError 处理分支吞掉，保留标准进程退出语义。
    def test_system_exit_propagates(self):
        self.write_env("DEEPSEEK_API_KEY=x\nSILICONFLOW_API_KEY=y\n")
        factory = RecordingFactory()
        with mock.patch("local_seed_demo.cli.DemoSeeder") as seeder:
            seeder.return_value.run.side_effect = SystemExit(7)
            with self.assertRaisesRegex(SystemExit, "7"):
                main(root=self.root, stdout=io.StringIO(), api_factory=factory)


if __name__ == "__main__":
    unittest.main()
