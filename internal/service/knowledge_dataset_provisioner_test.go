package service

import (
	"context"

	"oc-manager/internal/store/sqlc"
)

type knowledgeDatasetProvisionerStub struct {
	orgs []sqlc.Organization
	apps []sqlc.App
	err  error
}

func (s *knowledgeDatasetProvisionerStub) EnsureOrgDataset(_ context.Context, org sqlc.Organization) (sqlc.RagflowDataset, error) {
	s.orgs = append(s.orgs, org)
	return sqlc.RagflowDataset{}, s.err
}

func (s *knowledgeDatasetProvisionerStub) EnsureAppDataset(_ context.Context, app sqlc.App) (sqlc.RagflowDataset, error) {
	s.apps = append(s.apps, app)
	return sqlc.RagflowDataset{}, s.err
}
