package service

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAICCIP2RegionResolverLabelsPrivateAddresses 覆盖本地联调边界：
// 内网和回环地址不进入 IP 库查询，但会保存“本地网络”，避免管理端误显示未知地域。
func TestAICCIP2RegionResolverLabelsPrivateAddresses(t *testing.T) {
	resolver := &AICCIP2RegionResolver{}

	assert.Equal(t, "本地网络", resolver.Resolve(context.Background(), "127.0.0.1"))
	assert.Equal(t, "本地网络", resolver.Resolve(context.Background(), "10.0.0.8"))
	assert.Equal(t, "", resolver.Resolve(context.Background(), "192.0.2.1"))
	assert.Equal(t, "", resolver.Resolve(context.Background(), ""))
}

// TestFormatAICCIPRegion 覆盖 xdb region 格式转换：
// 国内地址优先展示城市，其次省份；海外地址展示国家，避免把 ISP 等历史字段暴露给用户。
func TestFormatAICCIPRegion(t *testing.T) {
	cases := []struct {
		name   string // 子场景说明
		raw    string
		expect string
	}{
		{name: "国内城市", raw: "中国|0|上海市|上海市|电信", expect: "上海市"},                               // 场景：省市都有值时展示城市。
		{name: "国内省份", raw: "中国|0|广东省|0|电信", expect: "广东省"},                                 // 场景：城市缺省时回退到省份。
		{name: "海外国家", raw: "United States|0|California|0|Google", expect: "United States"}, // 场景：非中国地址展示国家。
		{name: "空地域", raw: "0|0|0|0|0", expect: ""},                                         // 场景：库返回全 0 时不展示未知垃圾值。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, formatAICCIPRegion(tc.raw))
		})
	}
}

// TestExtractAICCGeoIPArchive 覆盖定期更新数据落盘：
// Gitee archive 中的 data/ip2region_v4.xdb 与 data/ip2region_v6.xdb 会被抽取到运行目录。
func TestExtractAICCGeoIPArchive(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"ip2region-master/data/ip2region_v4.xdb": "v4-data",
		"ip2region-master/data/ip2region_v6.xdb": "v6-data",
	} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	targetDir := t.TempDir()

	require.NoError(t, extractAICCGeoIPArchive(bytes.NewReader(buf.Bytes()), int64(buf.Len()), targetDir))

	v4, err := os.ReadFile(filepath.Join(targetDir, aiccGeoIPv4File))
	require.NoError(t, err)
	assert.Equal(t, "v4-data", string(v4))
	v6, err := os.ReadFile(filepath.Join(targetDir, aiccGeoIPv6File))
	require.NoError(t, err)
	assert.Equal(t, "v6-data", string(v6))
}

// TestValidateAICCGeoIPArchiveRejectsHTML 覆盖下载源异常返回 HTML 的场景：
// 更新器应在解包前给出明确错误，避免日志只显示底层 zip 解析失败。
func TestValidateAICCGeoIPArchiveRejectsHTML(t *testing.T) {
	err := validateAICCGeoIPArchive([]byte("<!DOCTYPE html>"), "text/html; charset=utf-8")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "不是有效 zip 文件")
	assert.Contains(t, err.Error(), "text/html")
}

// TestNewAICCGeoIPHTTPClientDisablesHTTP2 覆盖 Gitee archive 下载兼容性：
// 运行期更新固定使用 HTTP/1.1，避免 Go 默认 HTTP/2 请求被 Gitee 返回仓库 HTML 页面。
func TestNewAICCGeoIPHTTPClientDisablesHTTP2(t *testing.T) {
	client := newAICCGeoIPHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)

	assert.False(t, transport.ForceAttemptHTTP2)
	require.NotNil(t, transport.TLSClientConfig)
	assert.Equal(t, []string{"http/1.1"}, transport.TLSClientConfig.NextProtos)
}
