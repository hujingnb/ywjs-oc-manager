package config

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
)

// ResolveRuntimeImage 按 id 在镜像列表中查找对应 ref。
// 找到返回 (ref, true)；未找到返回 ("", false)。
func ResolveRuntimeImage(images []RuntimeImageConfig, id string) (string, bool) {
	for _, img := range images {
		if img.ID == id {
			return img.Ref, true
		}
	}
	return "", false
}

// ValidateRuntimeImages 校验镜像列表：id 非空且唯一、ref 非空。
// 空列表视为合法（Phase 1 不强制配置该段）。
func ValidateRuntimeImages(images []RuntimeImageConfig) error {
	seen := make(map[string]struct{}, len(images))
	for i, img := range images {
		if img.ID == "" {
			return fmt.Errorf("hermes.runtime_images[%d].id 不能为空", i)
		}
		if img.Ref == "" {
			return fmt.Errorf("hermes.runtime_images[%d].ref 不能为空", i)
		}
		if _, dup := seen[img.ID]; dup {
			return fmt.Errorf("hermes.runtime_images 存在重复 id: %s", img.ID)
		}
		seen[img.ID] = struct{}{}
	}
	return nil
}

// ValidateAICCRuntimeImage 校验客服专用镜像引用。
// 客服运行时必须独立发布，故拒绝空值、未带仓库路径以及浮动的无 tag/digest 引用，
// 避免 AICC 隐藏应用在运行期错误使用普通实例镜像或不可追溯的镜像版本。
func ValidateAICCRuntimeImage(image string) error {
	trimmed := strings.TrimSpace(image)
	if trimmed == "" {
		return fmt.Errorf("aicc.runtime_image 不能为空")
	}
	if trimmed != image {
		return fmt.Errorf("aicc.runtime_image 不能包含首尾空白")
	}
	named, err := reference.ParseNormalizedNamed(trimmed)
	if err != nil {
		return fmt.Errorf("aicc.runtime_image 非法: %w", err)
	}
	if !strings.Contains(reference.FamiliarName(named), "/") {
		return fmt.Errorf("aicc.runtime_image 必须包含独立仓库路径")
	}
	if tagged, ok := named.(reference.Tagged); ok {
		if tagged.Tag() == "latest" {
			return fmt.Errorf("aicc.runtime_image 不能使用 latest 浮动 tag")
		}
	} else {
		if _, digested := named.(reference.Digested); !digested {
			return fmt.Errorf("aicc.runtime_image 必须带不可变 tag 或 digest")
		}
	}
	return nil
}
