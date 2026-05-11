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

func TestPersona_GetCurrentMapsNoRowsToErrPersonaNotFound(t *testing.T) {
	stub := &personaStub{getErr: pgx.ErrNoRows}
	svc := NewPersonaService(stub)
	_, err := svc.GetCurrent(context.Background(), platformAdmin(), testPersonaOrgID)
	require.ErrorIs(t, err, ErrPersonaNotFound)
}

func TestPersona_GetCurrentDeniedForOtherOrg(t *testing.T) {
	stub := &personaStub{}
	svc := NewPersonaService(stub)
	_, err := svc.GetCurrent(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "other"}, testPersonaOrgID)
	require.ErrorIs(t, err, ErrPersonaDenied)
}

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

func TestPersona_ReplaceRejectsEmptyPrompt(t *testing.T) {
	stub := &personaStub{}
	svc := NewPersonaService(stub)
	_, err := svc.Replace(context.Background(), platformAdmin(), testPersonaOrgID, PersonaInput{SystemPrompt: ""})
	require.Error(t, err)
}

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
