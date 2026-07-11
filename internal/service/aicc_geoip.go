package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ip2region "github.com/lionsoul2014/ip2region/binding/golang/service"
)

const (
	// aiccGeoIPArchiveURL 是 GeoIP 数据更新源；使用国内 Gitee archive，避免构建和运行期依赖 GitHub。
	aiccGeoIPArchiveURL = "https://gitee.com/lionsoul/ip2region/repository/archive/master.zip"
	// aiccGeoIPBuiltinDir 是镜像构建阶段内置 xdb 数据的位置，启动时优先作为兜底数据源。
	aiccGeoIPBuiltinDir = "/usr/local/share/oc-manager/geoip"
	// aiccGeoIPRuntimeDir 是运行期定期更新后的 xdb 数据目录，不需要用户挂载配置文件。
	aiccGeoIPRuntimeDir = "/var/lib/oc-manager/data/geoip"
	// aiccGeoIPv4File 是 ip2region IPv4 数据库文件名。
	aiccGeoIPv4File = "ip2region_v4.xdb"
	// aiccGeoIPv6File 是 ip2region IPv6 数据库文件名。
	aiccGeoIPv6File = "ip2region_v6.xdb"
	// aiccGeoIPUpdateInterval 控制运行期自动拉取 GeoIP 数据的周期。
	aiccGeoIPUpdateInterval = 24 * time.Hour
	// aiccGeoIPArchiveMaxBytes 避免异常下载内容占用过多内存；当前 Gitee archive 约 26MiB。
	aiccGeoIPArchiveMaxBytes = 80 * 1024 * 1024
)

var aiccNonPublicIPPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// AICCGeoIPResolver 抽象公开会话地域解析能力，便于单测替换真实 IP 库。
type AICCGeoIPResolver interface {
	// Resolve 返回适合展示给运营人员的粗粒度地域；无法解析时返回空字符串。
	Resolve(ctx context.Context, remoteIP string) string
}

// AICCIP2RegionResolver 使用 ip2region xdb 数据库解析公开访客地域。
type AICCIP2RegionResolver struct {
	mu     sync.RWMutex
	db     *ip2region.Ip2Region
	client *http.Client
}

// NewAICCIP2RegionResolver 创建 GeoIP 解析器，并从运行期目录或镜像内置目录加载可用数据。
func NewAICCIP2RegionResolver() *AICCIP2RegionResolver {
	resolver := &AICCIP2RegionResolver{
		client: &http.Client{Timeout: 60 * time.Second},
	}
	_ = resolver.Reload()
	return resolver
}

// Resolve 将公网 IP 转为运营侧可读地域；私网、保留地址或库不可用时返回空字符串。
func (r *AICCIP2RegionResolver) Resolve(_ context.Context, remoteIP string) string {
	ip, ok := parseAICCPublicIP(remoteIP)
	if !ok {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.db == nil {
		return ""
	}
	region, err := r.db.Search(ip.String())
	if err != nil {
		return ""
	}
	return formatAICCIPRegion(region)
}

// Reload 重新加载当前可用的 xdb 文件；更新失败时不影响既有解析器继续使用旧数据。
func (r *AICCIP2RegionResolver) Reload() error {
	v4Path, v6Path := bestAICCGeoIPPaths()
	if v4Path == "" && v6Path == "" {
		return nil
	}
	next, err := ip2region.NewIp2RegionWithPath(v4Path, v6Path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	prev := r.db
	r.db = next
	r.mu.Unlock()
	if prev != nil {
		prev.CloseTimeout(time.Second)
	}
	return nil
}

// StartUpdater 在后台定期从国内源拉取最新 xdb 数据；失败只记录日志，不阻断主进程。
func (r *AICCIP2RegionResolver) StartUpdater(ctx context.Context, logger *slog.Logger) {
	go func() {
		r.updateAndLog(ctx, logger)
		ticker := time.NewTicker(aiccGeoIPUpdateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.updateAndLog(ctx, logger)
			}
		}
	}()
}

// UpdateFromArchive 下载并安装 GeoIP 数据，然后重新加载解析器。
func (r *AICCIP2RegionResolver) UpdateFromArchive(ctx context.Context) error {
	client := r.client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, aiccGeoIPArchiveURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("下载 AICC GeoIP 数据失败: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, aiccGeoIPArchiveMaxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > aiccGeoIPArchiveMaxBytes {
		return fmt.Errorf("AICC GeoIP 数据包超过大小限制")
	}
	parent := filepath.Dir(aiccGeoIPRuntimeDir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp(parent, "geoip-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	if err := extractAICCGeoIPArchive(bytes.NewReader(body), int64(len(body)), tmpDir); err != nil {
		return err
	}
	if err := os.MkdirAll(aiccGeoIPRuntimeDir, 0755); err != nil {
		return err
	}
	for _, name := range []string{aiccGeoIPv4File, aiccGeoIPv6File} {
		if err := os.Rename(filepath.Join(tmpDir, name), filepath.Join(aiccGeoIPRuntimeDir, name)); err != nil {
			return err
		}
	}
	return r.Reload()
}

func (r *AICCIP2RegionResolver) updateAndLog(ctx context.Context, logger *slog.Logger) {
	if err := r.UpdateFromArchive(ctx); err != nil && logger != nil && ctx.Err() != context.Canceled {
		logger.Warn("更新 AICC GeoIP 数据失败", "err", err)
	}
}

func bestAICCGeoIPPaths() (string, string) {
	for _, dir := range []string{aiccGeoIPRuntimeDir, aiccGeoIPBuiltinDir} {
		v4 := existingAICCGeoIPPath(filepath.Join(dir, aiccGeoIPv4File))
		v6 := existingAICCGeoIPPath(filepath.Join(dir, aiccGeoIPv6File))
		if v4 != "" || v6 != "" {
			return v4, v6
		}
	}
	return "", ""
}

func existingAICCGeoIPPath(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return ""
	}
	return path
}

func extractAICCGeoIPArchive(readerAt io.ReaderAt, size int64, targetDir string) error {
	zr, err := zip.NewReader(readerAt, size)
	if err != nil {
		return err
	}
	found := map[string]bool{}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	for _, file := range zr.File {
		name := filepath.Base(file.Name)
		if name != aiccGeoIPv4File && name != aiccGeoIPv6File {
			continue
		}
		if err := writeAICCGeoIPArchiveFile(file, filepath.Join(targetDir, name)); err != nil {
			return err
		}
		found[name] = true
	}
	for _, name := range []string{aiccGeoIPv4File, aiccGeoIPv6File} {
		if !found[name] {
			return fmt.Errorf("AICC GeoIP 数据包缺少 %s", name)
		}
	}
	return nil
}

func writeAICCGeoIPArchiveFile(file *zip.File, targetPath string) error {
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

func parseAICCPublicIP(remoteIP string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(remoteIP))
	if err != nil || !ip.IsValid() || ip.IsPrivate() {
		return netip.Addr{}, false
	}
	for _, prefix := range aiccNonPublicIPPrefixes {
		if prefix.Contains(ip) {
			return netip.Addr{}, false
		}
	}
	return ip, true
}

func formatAICCIPRegion(raw string) string {
	parts := strings.Split(strings.TrimSpace(raw), "|")
	country := aiccRegionPart(parts, 0)
	province := aiccRegionPart(parts, 2)
	city := aiccRegionPart(parts, 3)
	if country == "" {
		return ""
	}
	if country == "中国" {
		if city != "" {
			return city
		}
		return province
	}
	return country
}

func aiccRegionPart(parts []string, index int) string {
	if index >= len(parts) {
		return ""
	}
	value := strings.TrimSpace(parts[index])
	if value == "" || value == "0" {
		return ""
	}
	return value
}
