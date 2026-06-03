// client_skills.go — ocops 包 Hermes Skill 市场相关类型化客户端方法。
//
// 提供 4 个方法对接 oc-ops /oc/skills 端点，供 service 层在
// per-app 装卸 skill 时调用：
//   - SkillList：列出当前容器内所有 skill 状态（GET /oc/skills）
//   - SkillInstall：上传 skill 归档热装（POST /oc/skills，multipart）
//   - SkillDelete：热删指定 skill（DELETE /oc/skills/{name}）
//   - SkillReload：触发 hermes 重扫 skills 目录（POST /oc/skills/reload）
//
// SkillInstall 因需上传二进制归档文件，无法复用 DoJSON（JSON body），
// 改为手动构造 multipart/form-data 请求，鉴权与错误处理风格与 DoJSON 保持一致。
package ocops

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
)

// SkillList 列出 app 容器内所有 skill 的当前状态（GET /oc/skills）。
// 返回切片为空时表示容器内无任何 skill，不视为错误。
func (c *Client) SkillList(ctx context.Context, ep Endpoint) ([]SkillInfo, error) {
	// 用匿名结构体对应 {"skills":[...]} 的顶层包装
	var out struct {
		Skills []SkillInfo `json:"skills"`
	}
	if err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/skills", nil, &out); err != nil {
		return nil, err
	}
	return out.Skills, nil
}

// SkillDelete 热删容器内指定 skill（DELETE /oc/skills/{name}）。
// name 含特殊字符时由 url.PathEscape 编码，防止路径越界。
func (c *Client) SkillDelete(ctx context.Context, ep Endpoint, name string) error {
	return c.DoJSON(ctx, ep, http.MethodDelete, "/oc/skills/"+url.PathEscape(name), nil, nil)
}

// SkillReload 触发容器内 hermes 重扫 skills 目录使变更生效（POST /oc/skills/reload）。
// 不携带请求体，server 端执行扫描后返回 2xx 即为成功。
func (c *Client) SkillReload(ctx context.Context, ep Endpoint) error {
	return c.DoJSON(ctx, ep, http.MethodPost, "/oc/skills/reload", nil, nil)
}

// SkillInstall 把 skill 归档以 multipart/form-data 上传至容器热装（POST /oc/skills）。
//
// form 字段：
//   - name：skill 名称（文本字段）
//   - archive：归档文件字节（文件字段，filename 设为 name）
//
// 因需上传二进制文件，无法使用 DoJSON；鉴权（Bearer token）、非 2xx 错误映射
// 与 DoJSON 实现保持一致，非 2xx 状态码通过 statusToErr 统一转为哨兵错误。
func (c *Client) SkillInstall(ctx context.Context, ep Endpoint, name string, archive []byte) error {
	// 构造 multipart body：name 文本字段 + archive 文件字段
	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	// 写入 name 普通文本字段
	if err := w.WriteField("name", name); err != nil {
		return fmt.Errorf("ocops: 构造 multipart name 字段失败: %w", err)
	}

	// 写入 archive 文件字段（filename 设为 skill 名称）
	fw, err := w.CreateFormFile("archive", name)
	if err != nil {
		return fmt.Errorf("ocops: 构造 multipart archive 字段失败: %w", err)
	}
	if _, err := fw.Write(archive); err != nil {
		return fmt.Errorf("ocops: 写入 multipart 归档数据失败: %w", err)
	}

	// 必须在读取 body 前关闭 writer 以写入最终 boundary
	if err := w.Close(); err != nil {
		return fmt.Errorf("ocops: 关闭 multipart writer 失败: %w", err)
	}

	// 构造带 context 的 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.BaseURL+"/oc/skills", &body)
	if err != nil {
		return fmt.Errorf("ocops: 构造安装请求失败: %w", err)
	}

	// 设置 multipart Content-Type（含 boundary 参数）及 Bearer 鉴权头
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ep.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// 网络级错误与 DoJSON 保持一致：归入 ErrCLI
		return fmt.Errorf("%w: %v", ErrCLI, err)
	}
	defer resp.Body.Close()

	// 非 2xx：复用 statusToErr 映射哨兵错误
	if sentinel := statusToErr(resp.StatusCode); sentinel != nil {
		return sentinel
	}
	return nil
}
