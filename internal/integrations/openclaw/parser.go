package openclaw

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// ChannelLoginEvent 是从 OpenClaw runtime 解析出的登录事件统一表示。
//
// Sprint 0 POC 实测：上游 OpenClaw `channels login --channel openclaw-weixin --verbose`
// stdout 为中文提示 + 终端 ASCII QR + 回退 URL + "正在等待操作..."，**没有 JSON 协议**。
// parser 不解析 ASCII QR，只抓取回退 URL 作为 QRCode payload。前端用 URL 重新生成 PNG QR。
type ChannelLoginEvent struct {
	Type      string            `json:"type"`
	QRCode    string            `json:"qrcode,omitempty"`
	Code      string            `json:"code,omitempty"`
	Bound     string            `json:"bound_identity,omitempty"`
	Channel   string            `json:"channel_name,omitempty"`
	Error     string            `json:"error,omitempty"`
	ExpiresAt time.Time         `json:"expires_at,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// 与登录事件解析相关的错误。
var (
	// ErrUnparsableOutput 表示这一行不是任何已知的登录事件标识。
	// 调用方应继续读下一行。manager wechat.go 在收到此错误时不立即 fail，
	// 而是跳过当前行，等下一行尝试。
	ErrUnparsableOutput = errors.New("OpenClaw 输出不是登录事件协议行")

	// ErrEventExpired 表示成功解析为事件，但其 expires_at 已经过去。
	ErrEventExpired = errors.New("OpenClaw 登录事件已过期")
)

// 微信登录回退 URL 形如：
//
//	https://liteapp.weixin.qq.com/q/<id>?qrcode=<token>&bot_type=3
//
// docs 提示行 "若二维码未能显示或无法使用，你可以访问以下链接以继续：" 后跟 URL。
// 这里只匹配 URL 行本身，覆盖未来上游 URL host 可能轻微变化的情况。
var qrURLPattern = regexp.MustCompile(`https?://\S*?qrcode=[A-Za-z0-9_-]+\S*`)

// 二维码默认有效期。上游 stdout 没有显式 expires_at，默认 5 分钟（微信扫码常规寿命）。
// Sprint 2 实测后如发现真实有效期不同，调整此值。
const defaultQRTTL = 5 * time.Minute

// 关键词匹配（中文）。Sprint 2 真手机扫码后用实测文本替换或扩充。
var (
	keywordsExpired = []string{"二维码已过期", "二维码过期", "已失效", "已过期"}
	keywordsBound   = []string{"已连接微信账号", "登录成功", "已绑定", "Connected as"}
	keywordsFailed  = []string{"认证失败", "登录失败", "失败：", "Error:"}
	keywordsPending = []string{"正在等待操作", "等待扫描", "扫描成功，请在手机上确认"}
)

// ParseChannelLoginEvent 解析 OpenClaw stdout 单行文本。
//
// 解析顺序：
//  1. 匹配回退 URL → qrcode 事件（Sprint 0 POC 已确认）
//  2. 匹配过期关键词 → expired 事件
//  3. 匹配绑定成功关键词 → bound 事件，bound_identity 抽取（粗实现，Sprint 2 精化）
//  4. 匹配失败关键词 → failed 事件，error message 取整行
//  5. 匹配等待提示 → pending 事件（不携带额外信息，仅用于状态推进）
//  6. 其它（plugin loading log / ASCII QR 行 / 空行 / 中文提示行）→ ErrUnparsableOutput
//
// 调用方应在拿到 ErrUnparsableOutput 时继续读下一行。
func ParseChannelLoginEvent(line string) (ChannelLoginEvent, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ChannelLoginEvent{}, ErrUnparsableOutput
	}

	if match := qrURLPattern.FindString(trimmed); match != "" {
		return ChannelLoginEvent{
			Type:      "qrcode",
			QRCode:    match,
			ExpiresAt: time.Now().Add(defaultQRTTL),
		}, nil
	}

	if containsAny(trimmed, keywordsExpired) {
		return ChannelLoginEvent{Type: "expired"}, nil
	}

	if containsAny(trimmed, keywordsBound) {
		return ChannelLoginEvent{
			Type:    "bound",
			Bound:   extractBoundIdentity(trimmed),
			Channel: "openclaw-weixin",
		}, nil
	}

	if containsAny(trimmed, keywordsFailed) {
		return ChannelLoginEvent{
			Type:  "failed",
			Error: trimmed,
		}, nil
	}

	if containsAny(trimmed, keywordsPending) {
		return ChannelLoginEvent{Type: "pending"}, nil
	}

	return ChannelLoginEvent{}, ErrUnparsableOutput
}

// containsAny 检查 line 是否包含 keywords 中任一关键词。
func containsAny(line string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(line, kw) {
			return true
		}
	}
	return false
}

// extractBoundIdentity 从 "已连接微信账号 <name>" / "Connected as <name>" 这类行中抽出账号标识。
// 粗实现：取最后一个空格后的非空白序列；找不到则返回空。
// Sprint 2 实测后用真实绑定成功输出加正则细化。
func extractBoundIdentity(line string) string {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	last = strings.TrimRight(last, "。.,!?;:")
	return last
}
