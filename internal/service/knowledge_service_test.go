// Package service 的 knowledge_service_test 覆盖组织和应用知识库服务的权限、路径和同步状态逻辑。
package service

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/files"
	"oc-manager/internal/store/sqlc"
)

const (
	testKnowledgeOrg   = "00000000-0000-0000-0000-000000000e01"
	testKnowledgeOrg2  = "00000000-0000-0000-0000-000000000e02"
	testKnowledgeApp   = "00000000-0000-0000-0000-000000000e03"
	testKnowledgeOwner = "00000000-0000-0000-0000-000000000e04"
)

// TestKnowledgeServiceOpenOrgAllowsOrgMember 验证组织成员可下载本组织组织知识库文件。
func TestKnowledgeServiceOpenOrgAllowsOrgMember(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "docs/readme.md", strings.NewReader("hello"), 5))

	member := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "member-1"}
	stream, size, err := svc.OpenOrgFile(context.Background(), member, testKnowledgeOrg, "docs/readme.md")
	require.NoError(t, err)
	defer stream.Close()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)

	assert.Equal(t, int64(5), size)
	assert.Equal(t, "hello", string(body))
}

// TestKnowledgeServiceOpenOrgAllowsPlatformAdmin 验证平台管理员沿用组织知识库读取权限下载任意组织文件。
func TestKnowledgeServiceOpenOrgAllowsPlatformAdmin(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "policy.md", strings.NewReader("policy"), 6))

	stream, size, err := svc.OpenOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "policy.md")
	require.NoError(t, err)
	defer stream.Close()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)

	assert.Equal(t, int64(6), size)
	assert.Equal(t, "policy", string(body))
}

// TestKnowledgeServiceOpenAppAllowsPlatformAdmin 验证平台管理员沿用应用知识库读取权限下载任意实例文件。
func TestKnowledgeServiceOpenAppAllowsPlatformAdmin(t *testing.T) {
	svc := newKnowledgeService(t)
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	require.NoError(t, svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md", strings.NewReader("app"), 3))

	stream, size, err := svc.OpenAppFile(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md")
	require.NoError(t, err)
	defer stream.Close()
	body, err := io.ReadAll(stream)
	require.NoError(t, err)

	assert.Equal(t, int64(3), size)
	assert.Equal(t, "app", string(body))
}

// TestKnowledgeServiceOpenAppRejectsOtherOwner 验证组织成员不能下载其他成员的实例知识库文件。
func TestKnowledgeServiceOpenAppRejectsOtherOwner(t *testing.T) {
	svc := newKnowledgeService(t)
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	require.NoError(t, svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md", strings.NewReader("app"), 3))

	stranger := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}
	stream, size, err := svc.OpenAppFile(context.Background(), stranger, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md")

	require.ErrorIs(t, err, ErrKnowledgeForbidden)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenAppRejectsSpoofedOwner 验证下载接口不会信任客户端伪造的 owner_user_id 越权读取他人实例知识库。
func TestKnowledgeServiceOpenAppRejectsSpoofedOwner(t *testing.T) {
	svc := newKnowledgeService(t)
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	require.NoError(t, svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "secret.md", strings.NewReader("secret"), 6))

	attackerID := "00000000-0000-0000-0000-000000000e99"
	attacker := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: attackerID}
	stream, size, err := svc.OpenAppFile(context.Background(), attacker, testKnowledgeOrg, testKnowledgeApp, attackerID, "secret.md")

	require.ErrorIs(t, err, ErrKnowledgeForbidden)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenOrgRejectsEscapingPath 验证下载路径先按知识库子树校验，不能读取子树外同组织文件。
func TestKnowledgeServiceOpenOrgRejectsEscapingPath(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.master.Save(path.Join("org", testKnowledgeOrg, "secret.md"), strings.NewReader("secret"), 6))

	stream, size, err := svc.OpenOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "../../secret.md")

	require.ErrorIs(t, err, files.ErrInvalidPath)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenAppRejectsEscapingPath 验证应用级下载路径不能越过实例 knowledge 子目录读取外层文件。
func TestKnowledgeServiceOpenAppRejectsEscapingPath(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.master.Save(path.Join("org", testKnowledgeOrg, "app", "secret.md"), strings.NewReader("secret"), 6))

	stream, size, err := svc.OpenAppFile(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "../../secret.md")

	require.ErrorIs(t, err, files.ErrInvalidPath)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenRejectsEncodedTraversal 验证 URL 编码的 .. 仍按用户输入路径拦截，不能被前缀拼接掩盖。
func TestKnowledgeServiceOpenRejectsEncodedTraversal(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.master.Save(path.Join("org", testKnowledgeOrg, "secret.md"), strings.NewReader("secret"), 6))

	stream, size, err := svc.OpenOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "%2e%2e/%2e%2e/secret.md")

	require.ErrorIs(t, err, files.ErrInvalidPath)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenOrgRejectsDoubleEncodedTraversal 验证双重编码的 .. 不会在 SafeRoot 二次解码后越过组织 knowledge 子树。
func TestKnowledgeServiceOpenOrgRejectsDoubleEncodedTraversal(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.master.Save(path.Join("org", testKnowledgeOrg, "secret.md"), strings.NewReader("secret"), 6))

	stream, size, err := svc.OpenOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "%252e%252e/secret.md")

	require.ErrorIs(t, err, files.ErrInvalidPath)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenAppRejectsDoubleEncodedTraversal 验证双重编码的 .. 不会在 SafeRoot 二次解码后越过实例 knowledge 子树。
func TestKnowledgeServiceOpenAppRejectsDoubleEncodedTraversal(t *testing.T) {
	svc := newKnowledgeService(t)
	require.NoError(t, svc.master.Save(path.Join("org", testKnowledgeOrg, "app", testKnowledgeApp, "secret.md"), strings.NewReader("secret"), 6))

	stream, size, err := svc.OpenAppFile(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "%252e%252e/secret.md")

	require.ErrorIs(t, err, files.ErrInvalidPath)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceOpenRejectsAbsolutePath 验证下载路径拒绝绝对路径，避免用户输入绕过知识库相对路径契约。
func TestKnowledgeServiceOpenRejectsAbsolutePath(t *testing.T) {
	svc := newKnowledgeService(t)

	stream, size, err := svc.OpenOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "/secret.md")

	require.ErrorIs(t, err, files.ErrInvalidPath)
	require.Nil(t, stream)
	assert.Equal(t, int64(0), size)
}

// TestKnowledgeServiceRejectsTraversalForListSaveDelete 验证列表、上传、删除入口都先校验业务相对路径，不能在拼接租户前缀时吞掉 ..。
func TestKnowledgeServiceRejectsTraversalForListSaveDelete(t *testing.T) {
	svc := newKnowledgeService(t)
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	cases := []struct {
		name     string
		relative string
		run      func(string) error
	}{
		{name: "组织列表拒绝原始上级路径", relative: "../secret.md", run: func(rel string) error {
			_, err := svc.ListOrg(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel)
			return err
		}}, // 场景：组织列表不能用原始 .. 越出组织 knowledge 子树。
		{name: "组织列表拒绝编码上级路径", relative: "%2e%2e/secret.md", run: func(rel string) error {
			_, err := svc.ListOrg(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel)
			return err
		}}, // 场景：组织列表不能用 URL 编码 .. 越出组织 knowledge 子树。
		{name: "组织列表拒绝双重编码上级路径", relative: "%252e%252e/secret.md", run: func(rel string) error {
			_, err := svc.ListOrg(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel)
			return err
		}}, // 场景：组织列表不能用双重编码 .. 越出组织 knowledge 子树。
		{name: "组织上传拒绝原始上级路径", relative: "../secret.md", run: func(rel string) error {
			return svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel, strings.NewReader("x"), 1)
		}}, // 场景：组织上传不能用原始 .. 写到组织 knowledge 子树外。
		{name: "组织上传拒绝编码上级路径", relative: "%2e%2e/secret.md", run: func(rel string) error {
			return svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel, strings.NewReader("x"), 1)
		}}, // 场景：组织上传不能用 URL 编码 .. 写到组织 knowledge 子树外。
		{name: "组织上传拒绝双重编码上级路径", relative: "%252e%252e/secret.md", run: func(rel string) error {
			return svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel, strings.NewReader("x"), 1)
		}}, // 场景：组织上传不能用双重编码 .. 写到组织 knowledge 子树外。
		{name: "组织删除拒绝原始上级路径", relative: "../secret.md", run: func(rel string) error {
			return svc.DeleteOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel)
		}}, // 场景：组织删除不能用原始 .. 删除组织 knowledge 子树外文件。
		{name: "组织删除拒绝编码上级路径", relative: "%2e%2e/secret.md", run: func(rel string) error {
			return svc.DeleteOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel)
		}}, // 场景：组织删除不能用 URL 编码 .. 删除组织 knowledge 子树外文件。
		{name: "组织删除拒绝双重编码上级路径", relative: "%252e%252e/secret.md", run: func(rel string) error {
			return svc.DeleteOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, rel)
		}}, // 场景：组织删除不能用双重编码 .. 删除组织 knowledge 子树外文件。
		{name: "应用列表拒绝原始上级路径", relative: "../secret.md", run: func(rel string) error {
			_, err := svc.ListApp(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel)
			return err
		}}, // 场景：应用列表不能用原始 .. 越出应用 knowledge 子树。
		{name: "应用列表拒绝编码上级路径", relative: "%2e%2e/secret.md", run: func(rel string) error {
			_, err := svc.ListApp(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel)
			return err
		}}, // 场景：应用列表不能用 URL 编码 .. 越出应用 knowledge 子树。
		{name: "应用列表拒绝双重编码上级路径", relative: "%252e%252e/secret.md", run: func(rel string) error {
			_, err := svc.ListApp(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel)
			return err
		}}, // 场景：应用列表不能用双重编码 .. 越出应用 knowledge 子树。
		{name: "应用上传拒绝原始上级路径", relative: "../secret.md", run: func(rel string) error {
			return svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel, strings.NewReader("x"), 1)
		}}, // 场景：应用上传不能用原始 .. 写到应用 knowledge 子树外。
		{name: "应用上传拒绝编码上级路径", relative: "%2e%2e/secret.md", run: func(rel string) error {
			return svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel, strings.NewReader("x"), 1)
		}}, // 场景：应用上传不能用 URL 编码 .. 写到应用 knowledge 子树外。
		{name: "应用上传拒绝双重编码上级路径", relative: "%252e%252e/secret.md", run: func(rel string) error {
			return svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel, strings.NewReader("x"), 1)
		}}, // 场景：应用上传不能用双重编码 .. 写到应用 knowledge 子树外。
		{name: "应用删除拒绝原始上级路径", relative: "../secret.md", run: func(rel string) error {
			return svc.DeleteAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel)
		}}, // 场景：应用删除不能用原始 .. 删除应用 knowledge 子树外文件。
		{name: "应用删除拒绝编码上级路径", relative: "%2e%2e/secret.md", run: func(rel string) error {
			return svc.DeleteAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel)
		}}, // 场景：应用删除不能用 URL 编码 .. 删除应用 knowledge 子树外文件。
		{name: "应用删除拒绝双重编码上级路径", relative: "%252e%252e/secret.md", run: func(rel string) error {
			return svc.DeleteAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, rel)
		}}, // 场景：应用删除不能用双重编码 .. 删除应用 knowledge 子树外文件。
	}
	for _, tc := range cases {
		// 当前子测试覆盖 tc.name 描述的知识库路径穿越拒绝场景。
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(tc.relative)
			require.ErrorIs(t, err, files.ErrInvalidPath)
		})
	}
}

// TestKnowledgeServiceSaveOrgRequiresOrgManager 验证知识库服务保存组织要求组织Manager的预期行为场景。
func TestKnowledgeServiceSaveOrgRequiresOrgManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg2}, testKnowledgeOrg, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServiceSaveOrgWritesFile 验证知识库服务保存组织写入文件的成功路径场景。
func TestKnowledgeServiceSaveOrgWritesFile(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)
	listing, err := svc.ListOrg(context.Background(), platformAdmin(), testKnowledgeOrg, "")
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)
	assert.Equal(t, "doc.md", listing.Entries[0].Name)
}

// TestKnowledgeServiceSaveAppRespectsOwnership 验证知识库服务保存应用遵守Ownership的预期行为场景。
func TestKnowledgeServiceSaveAppRespectsOwnership(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.NoError(t, err)
}

// TestKnowledgeServicePlatformAdminCannotWriteOrgOrAppKnowledge 验证知识库服务平台管理员Cannot写入组织或应用知识库的预期行为场景。
func TestKnowledgeServicePlatformAdminCannotWriteOrgOrAppKnowledge(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.SaveAppFile(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "doc.md", strings.NewReader("hi"), 2)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServiceListAppRequiresAccess 验证知识库服务列表应用要求Access的预期行为场景。
func TestKnowledgeServiceListAppRequiresAccess(t *testing.T) {
	svc := newKnowledgeService(t)

	_, err := svc.ListApp(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "stranger"}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServicePlatformAdminCanReadOrgAndAppKnowledge 验证知识库服务平台管理员读取权限组织并应用知识库的预期行为场景。
func TestKnowledgeServicePlatformAdminCanReadOrgAndAppKnowledge(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)
	err = svc.SaveAppFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "app.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)

	orgListing, err := svc.ListOrg(context.Background(), platformAdmin(), testKnowledgeOrg, "")
	require.NoError(t, err)
	require.Len(t, orgListing.Entries, 1)
	assert.Equal(t, "doc.md", orgListing.Entries[0].Name)

	appListing, err := svc.ListApp(context.Background(), platformAdmin(), testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "")
	require.NoError(t, err)
	require.Len(t, appListing.Entries, 1)
	assert.Equal(t, "app.md", appListing.Entries[0].Name)
}

// TestKnowledgeServiceDeleteOrgRequiresManager 验证知识库服务删除组织要求Manager的预期行为场景。
func TestKnowledgeServiceDeleteOrgRequiresManager(t *testing.T) {
	svc := newKnowledgeService(t)

	err := svc.DeleteOrgFile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: "x"}, testKnowledgeOrg, "doc.md")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServicePlatformAdminCannotDeleteOrgOrRetrySync 验证知识库服务平台管理员Cannot删除组织或重试同步的预期行为场景。
func TestKnowledgeServicePlatformAdminCannotDeleteOrgOrRetrySync(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetRetryDispatcher(knowledgeRetryDispatcherStub{})

	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)

	err = svc.DeleteOrgFile(context.Background(), platformAdmin(), testKnowledgeOrg, "doc.md")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)

	err = svc.RetryOrgNodeSync(context.Background(), platformAdmin(), testKnowledgeOrg, "node-1")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServicePlatformAdminCannotGetOrgSyncStatus 验证知识库服务平台管理员Cannot获取组织同步状态的预期行为场景。
func TestKnowledgeServicePlatformAdminCannotGetOrgSyncStatus(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetSyncStatusSource(knowledgeStatusSourceStub{})

	_, err := svc.GetOrgSyncStatus(context.Background(), platformAdmin(), testKnowledgeOrg)
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestKnowledgeServiceOrgAdminCanGetOrgSyncStatus 验证知识库服务组织管理员权限判断获取组织同步状态的预期行为场景。
func TestKnowledgeServiceOrgAdminCanGetOrgSyncStatus(t *testing.T) {
	svc := newKnowledgeService(t)
	svc.SetSyncStatusSource(knowledgeStatusSourceStub{})

	_, err := svc.GetOrgSyncStatus(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg)
	require.NoError(t, err)
}

// TestKnowledgeServiceSaveOrgRecordsDispatchFailure 验证主副本写入成功但 dispatcher
// 入队失败时:1) 主流程不报错,接口仍返回成功;2) audit_logs 留下 failed 记录,
// 让运维能从审计页面发现"知识库已落主副本但未通知节点"的中间态。
func TestKnowledgeServiceSaveOrgRecordsDispatchFailure(t *testing.T) {
	svc := newKnowledgeService(t)
	auditor := &fakeKnowledgeAuditor{}
	svc.SetAuditor(auditor)
	svc.SetSyncDispatcher(failingDispatcher{err: errors.New("redis down")})

	// 主副本仍然写成功,接口对用户返回 nil:服务层契约保留"主副本是事实来源,同步是异步事项"。
	err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "doc.md", strings.NewReader("hello"), 5)
	require.NoError(t, err)

	// audit 留下一条 result=failed、action=dispatch_org_upload_file 的记录,排障可循。
	require.Len(t, auditor.events, 1)
	ev := auditor.events[0]
	assert.Equal(t, "knowledge_sync", ev.TargetType)
	assert.Equal(t, "failed", ev.Result)
	assert.Equal(t, "dispatch_org_upload_file", ev.Action)
	assert.Equal(t, testKnowledgeOrg, ev.OrgID)
	assert.Contains(t, ev.ErrorMessage, "redis down")
	// dispatch_org_upload_file 详情应包含「组织文件 <relPath>」便于审计列表识别。
	assert.Equal(t, "组织文件 doc.md", ev.DetailMessage)
}

// TestKnowledgeServiceDeleteAppRecordsDispatchFailure 覆盖应用级删除走相同
// 审计路径:确保不同 scope(app)+ change_type(delete)组合都不被静默吞掉。
func TestKnowledgeServiceDeleteAppRecordsDispatchFailure(t *testing.T) {
	svc := newKnowledgeService(t)
	auditor := &fakeKnowledgeAuditor{}
	svc.SetAuditor(auditor)
	svc.SetSyncDispatcher(failingDispatcher{err: errors.New("queue full")})

	// 先用 owner 身份写入一条主副本,确保 Delete 不因为目标缺失而提前返回。
	owner := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testKnowledgeOrg, UserID: testKnowledgeOwner}
	require.NoError(t, svc.SaveAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "a.md", strings.NewReader("x"), 1))
	auditor.events = nil // 清掉 Save 那次入队失败留下的痕迹,只断言 Delete 那次。

	err := svc.DeleteAppFile(context.Background(), owner, testKnowledgeOrg, testKnowledgeApp, testKnowledgeOwner, "a.md")
	require.NoError(t, err)

	require.Len(t, auditor.events, 1)
	ev := auditor.events[0]
	assert.Equal(t, "dispatch_app_delete_file", ev.Action)
	assert.Equal(t, "failed", ev.Result)
	assert.Equal(t, testKnowledgeApp, ev.TargetID) // app scope 时 target_id 用 app_id,方便按应用筛选。
	// app scope 时详情格式为「应用文件 <relPath>」。
	assert.Equal(t, "应用文件 a.md", ev.DetailMessage)
}

// failingDispatcher 给所有 Dispatch* 方法返回固定 err,用于触发 audit 路径。
type failingDispatcher struct{ err error }

func (f failingDispatcher) DispatchOrgChange(_ context.Context, _, _, _, _ string) error {
	return f.err
}

func (f failingDispatcher) DispatchAppChange(_ context.Context, _, _, _, _, _ string) error {
	return f.err
}

// fakeKnowledgeAuditor 收集 service 投递过来的 AuditEvent,供单测断言。
type fakeKnowledgeAuditor struct {
	events []AuditEvent
}

func (f *fakeKnowledgeAuditor) Record(_ context.Context, event AuditEvent) (AuditResult, error) {
	f.events = append(f.events, event)
	return AuditResult{}, nil
}

func newKnowledgeService(t *testing.T) *KnowledgeService {
	t.Helper()
	root, err := files.NewSafeRoot(t.TempDir(), 1024)
	require.NoError(t, err)
	svc := NewKnowledgeService(files.NewKnowledgeMaster(root))
	svc.SetAppStore(&fakeKnowledgeAppStore{apps: map[string]sqlc.App{
		testKnowledgeApp: {
			ID:          knowledgeTestUUID(t, testKnowledgeApp),
			OrgID:       knowledgeTestUUID(t, testKnowledgeOrg),
			OwnerUserID: knowledgeTestUUID(t, testKnowledgeOwner),
		},
	}})
	return svc
}

// fakeKnowledgeAppStore 为知识库服务测试提供 app_id 到真实归属的查询能力。
type fakeKnowledgeAppStore struct {
	apps map[string]sqlc.App
}

func (s *fakeKnowledgeAppStore) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	app, ok := s.apps[uuidToString(id)]
	if !ok {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return app, nil
}

func knowledgeTestUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	id, err := parseUUID(value)
	require.NoError(t, err)
	return id
}

func orgKnowledgeAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg, UserID: "00000000-0000-0000-0000-000000000e05"}
}

type knowledgeRetryDispatcherStub struct{}

func (knowledgeRetryDispatcherStub) RetryOrgNode(_ context.Context, _, _ string) error {
	return nil
}

type knowledgeStatusSourceStub struct{}

func (knowledgeStatusSourceStub) ListByOrg(_ context.Context, _ string) ([]SyncStatusResult, error) {
	return []SyncStatusResult{}, nil
}
