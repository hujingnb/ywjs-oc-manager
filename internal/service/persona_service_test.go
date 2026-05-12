// Package service 的 persona_service_test 覆盖组织人设读取、写入权限和版本化存储边界。
package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const testPersonaOrgID = "00000000-0000-0000-0000-000000003001"

// TestPersona_GetCurrentReturnsExisting 验证人设获取当前返回已有的成功路径场景。
func TestPersona_GetCurrentReturnsExisting(t *testing.T) {
	stub := &personaStub{
		persona: sqlc.OrganizationPersona{
			OrgID:               mustUUID(t, testPersonaOrgID),
			SystemPrompt:        "你是助手",
			Version:             3,
			AllowMemberOverride: true,
		},
	}
	svc := NewPersonaService(stub)
	result, err := svc.GetCurrent(context.Background(), platformAdmin(), testPersonaOrgID)
	require.NoError(t, err)
	if result.SystemPrompt != "你是助手" || result.Version != 3 || !result.AllowMemberOverride {
		t.Fatalf("result = %+v", result)
	}
}

// TestPersona_GetCurrentMapsNoRowsToErrPersonaNotFound 验证人设获取当前映射无Rows到错误人设未找到的异常或拒绝路径场景。
func TestPersona_GetCurrentMapsNoRowsToErrPersonaNotFound(t *testing.T) {
	stub := &personaStub{getErr: pgx.ErrNoRows}
	svc := NewPersonaService(stub)
	_, err := svc.GetCurrent(context.Background(), platformAdmin(), testPersonaOrgID)
	require.ErrorIs(t, err, ErrPersonaNotFound)
}

// TestPersona_GetCurrentDeniedForOtherOrg 验证人设获取当前Denied针对其他组织的预期行为场景。
func TestPersona_GetCurrentDeniedForOtherOrg(t *testing.T) {
	stub := &personaStub{}
	svc := NewPersonaService(stub)
	_, err := svc.GetCurrent(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "other"}, testPersonaOrgID)
	require.ErrorIs(t, err, ErrPersonaDenied)
}

// TestPersona_ReplaceWritesNewVersion 验证人设替换写入NewVersion的成功路径场景。
func TestPersona_ReplaceWritesNewVersion(t *testing.T) {
	stub := &personaStub{
		createResult: sqlc.OrganizationPersona{
			OrgID:        mustUUID(t, testPersonaOrgID),
			SystemPrompt: "新版本",
			Version:      4,
		},
	}
	svc := NewPersonaService(stub)
	result, err := svc.Replace(context.Background(), platformAdmin(), testPersonaOrgID, PersonaInput{
		SystemPrompt:        "新版本",
		AllowMemberOverride: true,
	})
	require.NoError(t, err)
	require.Equal(t, int32(4), result.Version)
	require.True(t, stub.createCalled)
}

// TestPersona_ReplaceRejectsEmptyPrompt 验证人设替换拒绝空值提示词的异常或拒绝路径场景。
func TestPersona_ReplaceRejectsEmptyPrompt(t *testing.T) {
	stub := &personaStub{}
	svc := NewPersonaService(stub)
	_, err := svc.Replace(context.Background(), platformAdmin(), testPersonaOrgID, PersonaInput{SystemPrompt: ""})
	require.Error(t, err)
}

// TestPersona_OrgAdminCanEditOwnOrg 验证人设组织管理员权限判断编辑本人组织的预期行为场景。
func TestPersona_OrgAdminCanEditOwnOrg(t *testing.T) {
	stub := &personaStub{
		createResult: sqlc.OrganizationPersona{OrgID: mustUUID(t, testPersonaOrgID), SystemPrompt: "x", Version: 1},
	}
	svc := NewPersonaService(stub)
	if _, err := svc.Replace(
		context.Background(),
		auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testPersonaOrgID, UserID: testRuntimeOpOwner},
		testPersonaOrgID,
		PersonaInput{SystemPrompt: "x"},
	); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// TestPersona_OrgMemberCannotEdit 验证人设组织成员Cannot编辑的预期行为场景。
func TestPersona_OrgMemberCannotEdit(t *testing.T) {
	stub := &personaStub{}
	svc := NewPersonaService(stub)
	_, err := svc.Replace(
		context.Background(),
		auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testPersonaOrgID},
		testPersonaOrgID,
		PersonaInput{SystemPrompt: "x"},
	)
	require.ErrorIs(t, err, ErrPersonaDenied)
}

type personaStub struct {
	persona      sqlc.OrganizationPersona
	getErr       error
	createResult sqlc.OrganizationPersona
	createErr    error
	createCalled bool
}

func (s *personaStub) GetCurrentOrganizationPersona(_ context.Context, _ pgtype.UUID) (sqlc.OrganizationPersona, error) {
	if s.getErr != nil {
		return sqlc.OrganizationPersona{}, s.getErr
	}
	return s.persona, nil
}

func (s *personaStub) CreateOrganizationPersona(_ context.Context, _ PersonaCreateInput) (sqlc.OrganizationPersona, error) {
	s.createCalled = true
	if s.createErr != nil {
		return sqlc.OrganizationPersona{}, s.createErr
	}
	return s.createResult, nil
}
