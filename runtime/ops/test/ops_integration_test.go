package ops_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/storage"
)

// opsTestEnv 从环境变量读 MinIO 接入参数与 ops 镜像 ref；缺失则跳过整组集成测。
type opsTestEnv struct {
	cfg   storage.S3Config
	image string
}

// loadOpsTestEnv 读取 OC_S3_TEST_* + OC_OPS_TEST_IMAGE；未设置 endpoint 即 Skip。
func loadOpsTestEnv(t *testing.T) opsTestEnv {
	t.Helper()
	ep := os.Getenv("OC_S3_TEST_ENDPOINT")
	if ep == "" {
		t.Skip("未设置 OC_S3_TEST_ENDPOINT，跳过 ops 集成测")
	}
	// STS role ARN 缺省给 MinIO 兼容占位值
	role := os.Getenv("OC_S3_TEST_STS_ROLE")
	if role == "" {
		role = "arn:aws:iam:::role/dev"
	}
	// ops 镜像缺省使用本地构建 tag
	img := os.Getenv("OC_OPS_TEST_IMAGE")
	if img == "" {
		img = "oc-manager-ops:dev"
	}
	return opsTestEnv{
		cfg: storage.S3Config{
			Endpoint:        ep,
			Region:          "us-east-1",
			Bucket:          os.Getenv("OC_S3_TEST_BUCKET"),
			AccessKeyID:     os.Getenv("OC_S3_TEST_AK"),
			SecretAccessKey: os.Getenv("OC_S3_TEST_SK"),
			UsePathStyle:    true,
			STSRoleARN:      role,
		},
		image: img,
	}
}

// bootstrapJSON 构造 mock bootstrap 返回的 canned 响应（含 skills 预签名 URL + 真实 STS s3_write 凭证）。
// 调用真实 STS 签发仅限于 appPrefix 范围内的临时写凭证，确保 oc-restore 可用 STS 凭证同步 workspace。
func bootstrapJSON(t *testing.T, env opsTestEnv, appPrefix, skillURL string) []byte {
	t.Helper()
	issuer := storage.NewSTSCredentialIssuer(env.cfg)
	// 签发 15 分钟的临时写凭证，覆盖测试运行周期
	creds, err := issuer.AssumeAppRole(context.Background(), appPrefix, 15*time.Minute)
	require.NoError(t, err)
	resp := map[string]any{
		"manifest_yaml": "version: \"2\"\napp:\n  id: it\n",
		"persona":       "测试 persona",
		"platform_rule": "测试 platform rule",
		"skills": []map[string]string{
			{"name": "weather", "rel_path": "resources/skills/weather.tar", "url": skillURL},
		},
		// s3_write 字段为 oc-restore 的 workspace sync 凭证（STS 临时凭证，限定 appPrefix）
		"s3_write": map[string]any{
			"endpoint":          env.cfg.Endpoint,
			"region":            env.cfg.Region,
			"bucket":            env.cfg.Bucket,
			"prefix":            appPrefix,
			"access_key_id":     creds.AccessKeyID,
			"secret_access_key": creds.SecretAccessKey,
			"session_token":     creds.SessionToken,
			"expires_at":        creds.ExpiresAt.UTC().Format(time.RFC3339),
		},
	}
	b, err := json.Marshal(resp)
	require.NoError(t, err)
	return b
}

// runOpsContainer 用 --network host 在 ops 容器内跑指定 command，挂载 data/input 目录。
// extraEnv 为附加环境变量（格式 "KEY=VALUE"）；返回 stdout+stderr 合并输出与执行错误。
func runOpsContainer(t *testing.T, env opsTestEnv, command, bootstrapURL, dataDir, inputDir string, extraEnv ...string) (string, error) {
	t.Helper()
	args := []string{
		"run", "--rm", "--network", "host",
		"-e", "OC_CONTROL_TOKEN=test-token",
		"-e", "OC_BOOTSTRAP_URL=" + bootstrapURL,
		"-e", "OC_DATA_DIR=/data",
		"-e", "OC_INPUT_DIR=/input",
		"-e", "HOME=/tmp",
		"-v", dataDir + ":/data",
		"-v", inputDir + ":/input",
	}
	for _, e := range extraEnv {
		args = append(args, "-e", e)
	}
	args = append(args, env.image, command)
	cmd := exec.Command("docker", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// TestOcRestore 验证 oc-restore 在已有 app 数据场景下的完整恢复流程：
// 写 manifest/persona/skills 到 input 目录、用 STS 凭证 sync workspace、
// 恢复 state.db 且清理 -wal/-shm 文件。
func TestOcRestore(t *testing.T) {
	env := loadOpsTestEnv(t)
	store := storage.NewS3ObjectStore(env.cfg)
	ctx := context.Background()
	// 为本次测试生成唯一 appID，避免并发污染
	id := fmt.Sprintf("it-restore-%d", time.Now().UnixNano())
	appPrefix := storage.AppPrefix(id) // apps/<id>/
	// 测试结束后清理 S3 上的 app 数据
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), appPrefix) })

	// 预置：version 级 skill 对象（供预签名）+ apps/<id>/ 下 workspace 对象与 state.db
	skillKey := storage.SkillKey("itv", "weather")
	require.NoError(t, store.PutObject(ctx, skillKey, strings.NewReader("SKILL-TAR"), int64(len("SKILL-TAR"))))
	// 清理 version 级对象（跨测试共享 key，避免残留）
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), "versions/itv/") })
	skillURL, err := store.PresignGet(ctx, skillKey, 10*time.Minute)
	require.NoError(t, err)
	// 预置 workspace 文件与 state.db
	require.NoError(t, store.PutObject(ctx, appPrefix+"workspace/hello.txt", strings.NewReader("WS"), 2))
	require.NoError(t, store.PutObject(ctx, storage.StateDBKey(id), strings.NewReader("SQLITEDATA"), 10))

	// 起 mock bootstrap，校验 Authorization header 并返回 canned 响应
	body := bootstrapJSON(t, env, appPrefix, skillURL)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验 control token 是否正确传递
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	inputDir := t.TempDir()
	out, runErr := runOpsContainer(t, env, "oc-restore", srv.URL+"/internal/apps/"+id+"/bootstrap", dataDir, inputDir)
	require.NoError(t, runErr, "oc-restore 容器执行失败:\n%s", out)

	// 断言：manifest/persona/skills 落 input 目录
	assertFileContains(t, filepath.Join(inputDir, "manifest.yaml"), "app:")
	assertFileContains(t, filepath.Join(inputDir, "resources/persona.md"), "测试 persona")
	assertFileContains(t, filepath.Join(inputDir, "resources/platform-rules.md"), "测试 platform rule")
	assertFileContains(t, filepath.Join(inputDir, "resources/skills/weather.tar"), "SKILL-TAR")
	// 断言：workspace sync 下来到 data 目录
	assertFileContains(t, filepath.Join(dataDir, "workspace/hello.txt"), "WS")
	// 断言：state.db 恢复且 -wal/-shm 两个 WAL 边车都被清理（保证干净重开）
	assertFileContains(t, filepath.Join(dataDir, "state.db"), "SQLITEDATA")
	assert.NoFileExists(t, filepath.Join(dataDir, "state.db-wal"))
	assert.NoFileExists(t, filepath.Join(dataDir, "state.db-shm"))
}

// TestOcRestoreFirstBoot 验证首启场景（apps/<id>/ 前缀为空）：
// workspace sync 空操作、state.db 跳过恢复、脚本整体不报错。
func TestOcRestoreFirstBoot(t *testing.T) {
	env := loadOpsTestEnv(t)
	store := storage.NewS3ObjectStore(env.cfg)
	ctx := context.Background()
	// 首启场景：app 前缀下不预置任何对象
	id := fmt.Sprintf("it-firstboot-%d", time.Now().UnixNano())
	appPrefix := storage.AppPrefix(id)

	// 仍需预置 skill 对象以生成有效预签名（bootstrap 响应需包含可下载 URL）
	skillKey := storage.SkillKey("itv2", "weather")
	require.NoError(t, store.PutObject(ctx, skillKey, strings.NewReader("S"), 1))
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), "versions/itv2/") })
	skillURL, err := store.PresignGet(ctx, skillKey, 10*time.Minute)
	require.NoError(t, err)

	// mock bootstrap 直接返回，不校验 token（首启场景只验主流程不出错）
	body := bootstrapJSON(t, env, appPrefix, skillURL)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dataDir := t.TempDir()
	inputDir := t.TempDir()
	out, runErr := runOpsContainer(t, env, "oc-restore", srv.URL+"/internal/apps/"+id+"/bootstrap", dataDir, inputDir)
	// 首启时 S3 前缀为空，oc-restore 应静默完成而非报错
	require.NoError(t, runErr, "首启 oc-restore 应成功:\n%s", out)
	// 首启无 state.db 可恢复
	assert.NoFileExists(t, filepath.Join(dataDir, "state.db"))
}

// assertFileContains 断言指定路径的文件存在且包含 want 子串。
func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "读文件 %s", path)
	assert.Contains(t, string(b), want)
}
