package files

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KnowledgeEntry 描述知识库主副本中的一个条目。
type KnowledgeEntry struct {
	Path  string
	Name  string
	Size  int64
	IsDir bool
}

// KnowledgeMaster 维护 manager 本地的“主副本”目录。
// 上传/删除/列表都先落到本地，再通过 worker 同步到 runtime node 上的应用工作目录。
type KnowledgeMaster struct {
	root *SafeRoot
}

// NewKnowledgeMaster 创建主副本管理器。
func NewKnowledgeMaster(root *SafeRoot) *KnowledgeMaster {
	return &KnowledgeMaster{root: root}
}

// 与知识库写入相关的错误。
var (
	ErrKnowledgePathRequired = errors.New("知识库路径不能为空")
)

// Save 写入文件内容。
// content 必须能被全部读完；写入前会创建必要的目录。
func (m *KnowledgeMaster) Save(relative string, content io.Reader, size int64) error {
	if relative == "" {
		return ErrKnowledgePathRequired
	}
	if err := m.root.EnsureSizeWithinLimit(size); err != nil {
		return err
	}
	resolved, err := m.root.Resolve(relative)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return fmt.Errorf("创建知识库目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), ".tmp-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	written, copyErr := io.Copy(tmp, io.LimitReader(content, m.root.MaxFileSize+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("写入临时文件失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("关闭临时文件失败: %w", closeErr)
	}
	if written > m.root.MaxFileSize {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("%w: 实际写入 %d 字节", ErrFileTooLarge, written)
	}
	if err := os.Rename(tmp.Name(), resolved); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("发布知识库文件失败: %w", err)
	}
	return nil
}

// Delete 删除知识库主副本中的文件或目录。
// 如果目标不存在直接返回 nil（幂等）。
func (m *KnowledgeMaster) Delete(relative string) error {
	if relative == "" {
		return ErrKnowledgePathRequired
	}
	resolved, err := m.root.Resolve(relative)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(resolved); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return os.RemoveAll(resolved)
}

// List 列出指定相对路径下的条目。
func (m *KnowledgeMaster) List(relative string) ([]KnowledgeEntry, error) {
	if relative == "" {
		relative = "."
	}
	if relative == "." {
		return readEntries(m.root.Root, "")
	}
	resolved, err := m.root.Resolve(relative)
	if err != nil {
		return nil, err
	}
	return readEntries(resolved, strings.TrimPrefix(relative, "/"))
}

func readEntries(absDir, relPrefix string) ([]KnowledgeEntry, error) {
	entries, err := os.ReadDir(absDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []KnowledgeEntry{}, nil
		}
		return nil, err
	}
	results := make([]KnowledgeEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		var rel string
		if relPrefix == "" {
			rel = entry.Name()
		} else {
			rel = filepath.ToSlash(filepath.Join(relPrefix, entry.Name()))
		}
		results = append(results, KnowledgeEntry{
			Path:  rel,
			Name:  entry.Name(),
			Size:  info.Size(),
			IsDir: entry.IsDir(),
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].IsDir != results[j].IsDir {
			return results[i].IsDir && !results[j].IsDir
		}
		return results[i].Name < results[j].Name
	})
	return results, nil
}
