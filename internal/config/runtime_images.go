package config

import "fmt"

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
