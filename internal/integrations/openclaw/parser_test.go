package openclaw

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

// 真实 stdout 样本（Sprint 0 POC 实测）。
const realQRURLLine = "https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=85e18acc56ebd5937ad4caa5fe1b01a1&bot_type=3"

// TestParseChannelLoginEventQRCodeFromRealUpstream 验证解析渠道登录事件二维码标识来自真实Upstream的预期行为场景。
func TestParseChannelLoginEventQRCodeFromRealUpstream(t *testing.T) {
	event, err := ParseChannelLoginEvent(realQRURLLine)
	require.NoError(t, err)
	require.Equal(t, "qrcode", event.Type)
	require.Equal(t, realQRURLLine, event.QRCode)
	if event.ExpiresAt.IsZero() {
		t.Fatalf("event.ExpiresAt should default to now+5min")
	}
	if time.Until(event.ExpiresAt) > 6*time.Minute {
		t.Fatalf("event.ExpiresAt too far in future: %v", event.ExpiresAt)
	}
}

// TestParseChannelLoginEventQRCodeWithLeadingPrompt 验证解析渠道登录事件二维码标识使用Leading提示词的预期行为场景。
func TestParseChannelLoginEventQRCodeWithLeadingPrompt(t *testing.T) {
	// 容忍 docs 提示行直接跟 URL 的情况："…链接以继续：\nhttps://…"。
	// parser 是按行调用的，所以提示行与 URL 行分别走不同 ParseChannelLoginEvent 调用。
	// 这里只保证 URL 行本身被识别。
	line := "    " + realQRURLLine + "    "
	event, err := ParseChannelLoginEvent(line)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(event.QRCode, "https://"))
}

// TestParseChannelLoginEventBoundFromRealUpstream 验证解析渠道登录事件已绑定来自真实Upstream的预期行为场景。
func TestParseChannelLoginEventBoundFromRealUpstream(t *testing.T) {
	// Sprint 0 真手机扫码实测样本：上游绑定成功**唯一**关键行。
	// 注意结尾有句号；不携带任何账号标识。
	line := "已将此 OpenClaw 连接到微信。"
	event, err := ParseChannelLoginEvent(line)
	require.NoError(t, err)
	require.Equal(t, "bound", event.Type)
	require.Equal(t, "openclaw-weixin", event.Channel)
	// stdout 不携带 userId/wxid，service 层负责后续从 plugin state 补齐。
	require.Equal(t, "", event.Bound)
}

// TestParseChannelLoginEventBoundEnglishFallback 验证解析渠道登录事件已绑定English回退的特殊分支或幂等场景。
func TestParseChannelLoginEventBoundEnglishFallback(t *testing.T) {
	// 关键词列表保留英文 fallback，应对未来上游加英文输出。
	line := "Connected this OpenClaw to WeChat."
	event, err := ParseChannelLoginEvent(line)
	require.NoError(t, err)
	require.Equal(t, "bound", event.Type)
}

// TestParseChannelLoginEventExpired 验证解析渠道登录事件过期的异常或拒绝路径场景。
func TestParseChannelLoginEventExpired(t *testing.T) {
	for _, line := range []string{"二维码已过期", "二维码过期，请重新尝试", "已失效"} {
		event, err := ParseChannelLoginEvent(line)
		require.NoError(t, err)
		require.Equal(t, "expired", event.Type)
	}
}

// TestParseChannelLoginEventFailed 验证解析渠道登录事件失败的预期行为场景。
func TestParseChannelLoginEventFailed(t *testing.T) {
	line := "认证失败：账号被冻结"
	event, err := ParseChannelLoginEvent(line)
	require.NoError(t, err)
	require.Equal(t, "failed", event.Type)
	require.NotEqual(t, "", event.Error)
}

// TestParseChannelLoginEventPending 验证解析渠道登录事件等待中的预期行为场景。
func TestParseChannelLoginEventPending(t *testing.T) {
	for _, line := range []string{"正在等待操作...", "扫描成功，请在手机上确认", "等待扫描"} {
		event, err := ParseChannelLoginEvent(line)
		require.NoError(t, err)
		require.Equal(t, "pending", event.Type)
	}
}

// TestParseChannelLoginEventRejectsNoise 验证解析渠道登录事件拒绝Noise的异常或拒绝路径场景。
func TestParseChannelLoginEventRejectsNoise(t *testing.T) {
	// plugin loading log / ASCII QR 行 / 中文提示行 / 空行 都应该返回 ErrUnparsableOutput
	// 让调用方继续读下一行。
	cases := []string{
		"",
		"   ",
		"[plugins] loading anthropic from /root/.openclaw/...",
		"[plugins] loaded 118 plugin(s) (70 attempted) in 11035.8ms",
		"正在启动...", // 此前是 startup 提示，不携带数据
		"用手机微信扫描以下二维码，以继续连接：",                     // 提示行，下一行才是 ASCII QR
		"▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄", // ASCII QR 行
		"█ ▄▄▄▄▄ █ ██▀▀ ▄▄▄█ ▀█▄▄▄▄▄█▄▀█ ▄▄▄▄▄ █", // ASCII QR 行
		"若二维码未能显示或无法使用，你可以访问以下链接以继续：",             // URL 之前的提示行
		"Welcome to OpenClaw", // legacy 噪声
		"{}",                  // 空 JSON object
	}
	for _, line := range cases {
		_, err := ParseChannelLoginEvent(line)
		require.ErrorIs(t, err, ErrUnparsableOutput)
	}
}

// TestParseChannelLoginEventQRCodeHostVariant 验证解析渠道登录事件二维码标识HostVariant的预期行为场景。
func TestParseChannelLoginEventQRCodeHostVariant(t *testing.T) {
	// 容忍上游未来 host 变化（如换成 weixin.qq.com 直链），只要包含 ?qrcode= 都识别。
	line := "https://example.weixin.qq.com/some/path?qrcode=abc123def456&extra=1"
	event, err := ParseChannelLoginEvent(line)
	require.NoError(t, err)
	if event.Type != "qrcode" || !strings.Contains(event.QRCode, "qrcode=abc123") {
		t.Fatalf("event=%+v", event)
	}
}
