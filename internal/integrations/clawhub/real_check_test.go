package clawhub

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestRealClawHubcn 用 clawhub client 直连真实 clawhubcn.com，验证适配后的 schema 解析正确、
// 能下到真实 zip。需宿主外网（k3d pod 无外网 DNS，故只在宿主跑），OCM_CLAWHUB_REAL=1 守门避免 CI 误触。
func TestRealClawHubcn(t *testing.T) {
	if os.Getenv("OCM_CLAWHUB_REAL") != "1" {
		t.Skip("需 OCM_CLAWHUB_REAL=1 + 宿主外网访问 clawhubcn.com")
	}
	c := NewClient("https://clawhubcn.com", 30*time.Second)

	// 列表：验证 items/displayName/summary/tags.latest/stats.downloads 映射到扁平 Skill。
	res, err := c.Search(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Search 失败: %v", err)
	}
	if len(res.Skills) == 0 {
		t.Fatal("Search 返回空（schema 未对上）")
	}
	sk := res.Skills[0]
	t.Logf("首个 skill: slug=%s name=%q version=%s downloads=%d", sk.Slug, sk.Name, sk.Version, sk.Downloads)
	if sk.Slug == "" || sk.Name == "" || sk.Version == "" {
		t.Fatalf("字段映射失败: %+v", sk)
	}

	// 带关键词搜索：clawhubcn 用 /api/v1/skills?q= 过滤（无独立 /api/v1/search 端点，
	// 早期 client 误打该路径会被 reset）。用 "self" 这种已知有前缀匹配的词，断言非空。
	hit, err := c.Search(context.Background(), "self", "")
	if err != nil {
		t.Fatalf("带 q 的 Search 失败（疑似又打到 /api/v1/search）: %v", err)
	}
	if len(hit.Skills) == 0 {
		t.Fatal("Search(q=self) 返回空——搜索端点/参数未对上")
	}
	t.Logf("Search(q=self) 命中 %d 个，首个=%s", len(hit.Skills), hit.Skills[0].Slug)

	// 版本：验证 {items:[{version}]} 解包。
	vs, err := c.ListVersions(context.Background(), sk.Slug)
	if err != nil {
		t.Fatalf("ListVersions 失败: %v", err)
	}
	t.Logf("ListVersions(%s) 返回 %d 个版本", sk.Slug, len(vs))

	// 详情：验证 {skill,latestVersion,owner,metadata} 解包出富字段。
	detail, err := c.GetSkill(context.Background(), sk.Slug)
	if err != nil {
		t.Fatalf("GetSkill 失败: %v", err)
	}
	t.Logf("GetSkill(%s): name=%q version=%s 描述长度=%d 作者=%q 下载=%d 星标=%d",
		detail.Slug, detail.Name, detail.Version, len(detail.Description), detail.AuthorName, detail.Downloads, detail.Stars)
	if detail.Name == "" || detail.Description == "" {
		t.Fatalf("详情富字段解析失败: %+v", detail)
	}

	// 下载：skill-vetter@1.0.0 是已知可下的真实 skill，验证返回真实 zip（PK 魔数）。
	data, err := c.Download(context.Background(), "skill-vetter", "1.0.0")
	if err != nil {
		t.Fatalf("Download 失败: %v", err)
	}
	t.Logf("Download skill-vetter@1.0.0: %d 字节, 前4=%v", len(data), data[:4])
	if len(data) < 4 || data[0] != 'P' || data[1] != 'K' {
		t.Fatal("下载内容非 zip（PK 魔数缺失）")
	}
}
