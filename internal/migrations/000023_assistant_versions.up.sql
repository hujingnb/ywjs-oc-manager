-- 000023_assistant_versions.up.sql
-- 助手版本目录表，以及组织 allowlist / 实例绑定版本所需的关联列。
-- 本迁移全部为 additive：不删除任何已有表或列，保证构建与运行不被打断。

CREATE TABLE assistant_versions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    system_prompt text NOT NULL,
    image_id      text NOT NULL,
    main_model    text NOT NULL,
    routing_json  jsonb NOT NULL DEFAULT '{}'::jsonb,
    skills_json   jsonb NOT NULL DEFAULT '[]'::jsonb,
    revision      integer NOT NULL DEFAULT 1,
    created_by    uuid NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz NULL,
    CONSTRAINT assistant_versions_revision_check CHECK (revision > 0)
);

COMMENT ON TABLE assistant_versions IS '助手版本目录：把智能路由、skill、内置提示词、镜像打包成可复用的命名版本。';
COMMENT ON COLUMN assistant_versions.system_prompt IS '版本内置提示词，渲染进容器 SOUL.md 的版本层；字段语义泛化，可填写人设、行为规则等。';
COMMENT ON COLUMN assistant_versions.image_id IS '引用配置文件 hermes.runtime_images[].id。';
COMMENT ON COLUMN assistant_versions.routing_json IS '智能路由：8 个 auxiliary 槽位到模型名的映射，空槽位省略。';
COMMENT ON COLUMN assistant_versions.skills_json IS 'skill 元信息数组，每项 {name,file_path,file_size,file_sha256}；tar 字节存文件系统主副本。';
COMMENT ON COLUMN assistant_versions.revision IS '内容修订号，影响容器的字段变更时 +1，供实例 version_synced 检测使用。';

-- 版本名在未删除集合内唯一。
CREATE UNIQUE INDEX assistant_versions_name_active_idx
    ON assistant_versions(name) WHERE deleted_at IS NULL;

-- 组织可用版本 allowlist：jsonb 字符串数组。
ALTER TABLE organizations
    ADD COLUMN assistant_version_ids jsonb NOT NULL DEFAULT '[]'::jsonb;
COMMENT ON COLUMN organizations.assistant_version_ids IS '该组织可用的助手版本 id 数组（allowlist）。';

-- 实例绑定的版本与变更检测字段。version_id 暂可空，Phase 3 创建流程改造后由 service 强制必填。
ALTER TABLE apps
    ADD COLUMN version_id uuid NULL REFERENCES assistant_versions(id),
    ADD COLUMN applied_version_revision integer NOT NULL DEFAULT 0,
    ADD COLUMN applied_image_ref text NOT NULL DEFAULT '';
COMMENT ON COLUMN apps.version_id IS '实例绑定的助手版本。';
COMMENT ON COLUMN apps.applied_version_revision IS '上次初始化/重启时使用的版本 revision；与版本当前 revision 比较得出 version_synced。';
COMMENT ON COLUMN apps.applied_image_ref IS '上次实际拉取的镜像 ref；与配置解析出的 ref 比较得出 version_synced。';

CREATE INDEX apps_version_id_idx ON apps(version_id);
