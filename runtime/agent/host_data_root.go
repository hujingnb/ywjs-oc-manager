package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// detectHostDataRoot 通过读取 /proc/self/mountinfo 反推 agent 容器内 dataRoot
// 对应的宿主路径。
//
// agent 容器以 bind mount 形式持有 dataRoot（容器内默认 /var/lib/oc-agent），
// 实际宿主上指向另一处（例如 /home/.../oc-manager/.local/data/agent）。
// docker daemon 在宿主视角解析 mount source；manager 通过 docker proxy 创建
// ocm-* 容器时传的是 agent 视角路径，docker daemon 看不到这条路径就会创建
// 空目录占位，文件级 mount 直接退化成空目录，legacy OpenClaw 读不到 models.json
//（Hermes 时代已不再使用 file-level mount，改为全量挂载，但此函数仍用于路径重写）。
//
// detectHostDataRoot 在 agent 启动时一次性确定宿主真实路径，docker proxy
// 后续转发 create container 请求时把 agent 视角的 mount source 替换成宿主路径。
//
// mountinfo 字段（man procfs(5) §3.5）：
//
//	mountID parentID major:minor rootOnHost mountPoint mountOpts ...
//
// 我们要的是 mountPoint == agentDataRoot 的 rootOnHost。
//
// 失败语义：
//   - agent 不在容器中 / dataRoot 不是 mount → 返回原值（视作宿主即 agent 视角）；
//     这种 dev 场景（直接 go run）下 manager 传的 path 已经在宿主，无需重写。
//   - 文件读失败 → 返回错误，调用方决定 fail-fast 还是降级。
func detectHostDataRoot(agentDataRoot string) (string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		// 非 Linux 或 procfs 不可用：退化为不重写。
		return agentDataRoot, nil
	}
	defer f.Close()
	host, err := parseHostDataRootFromMountInfo(f, agentDataRoot)
	if err != nil {
		return "", err
	}
	if host == "" {
		// 未找到匹配 mount，认为 dataRoot 没被 bind mount，原值即宿主路径。
		return agentDataRoot, nil
	}
	return host, nil
}

// parseHostDataRootFromMountInfo 仅做 mountinfo 行解析；分离出来便于单测。
//
// 一行 mountinfo 至少 7 个空格分隔字段；rootOnHost 在第 4 个，mountPoint 在第 5 个。
// mountPoint 用 octal 转义（空格 \040 / 制表 \011 / 反斜杠 \134），需要还原。
func parseHostDataRootFromMountInfo(r io.Reader, agentDataRoot string) (string, error) {
	scanner := bufio.NewScanner(r)
	// mountinfo 单行可能很长（dataRoot 含长路径），调大 buffer。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		rootOnHost := unescapeMountInfoField(fields[3])
		mountPoint := unescapeMountInfoField(fields[4])
		if mountPoint == agentDataRoot {
			return rootOnHost, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取 mountinfo 失败: %w", err)
	}
	return "", nil
}

// unescapeMountInfoField 还原 mountinfo 字段里的 octal 转义。
// 仅处理 \040 \011 \012 \134（空格 / 制表 / 换行 / 反斜杠）这几种 procfs 实际产出的转义。
func unescapeMountInfoField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' && i+3 < len(s) {
			// 期待三位八进制
			c1, c2, c3 := s[i+1], s[i+2], s[i+3]
			if isOctal(c1) && isOctal(c2) && isOctal(c3) {
				v := byte((c1-'0')*64 + (c2-'0')*8 + (c3 - '0'))
				b.WriteByte(v)
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isOctal(c byte) bool { return c >= '0' && c <= '7' }
