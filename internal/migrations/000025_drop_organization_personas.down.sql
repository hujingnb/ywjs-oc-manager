-- 000025_drop_organization_personas.down.sql
-- 回滚：恢复 organization_personas 表，与 000002_core_schema.up.sql 中原始 DDL 完全一致。
CREATE TABLE organization_personas (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES organizations(id),
    system_prompt text NOT NULL,
    conversation_rules text NULL,
    forbidden_rules text NULL,
    reply_style text NULL,
    allow_member_override boolean NOT NULL DEFAULT false,
    version integer NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT organization_personas_version_check CHECK (version > 0),
    CONSTRAINT organization_personas_org_version_unique UNIQUE (org_id, version)
);

COMMENT ON TABLE organization_personas IS '组织级 AI 人设版本表，当前生效版本取同组织最大 version。';
COMMENT ON COLUMN organization_personas.allow_member_override IS '是否允许成员应用使用 app_prompt 覆盖组织默认人设。';

CREATE INDEX organization_personas_org_version_idx ON organization_personas(org_id, version DESC);
