CREATE TABLE skill_tickets (
    id                 CHAR(36)     NOT NULL                 COMMENT '主键 UUID',
    org_id             CHAR(36)     NOT NULL                 COMMENT '提交者所属组织,定向范围锚点',
    requester_user_id  CHAR(36)     NOT NULL                 COMMENT '提交者 user id',
    requester_role     VARCHAR(32)  NOT NULL                 COMMENT '提交时角色快照:org_admin|org_member,决定默认目标范围',
    title              VARCHAR(255) NOT NULL                 COMMENT '需求标题',
    description        TEXT         NOT NULL                 COMMENT '需求描述/使用场景/输入输出示例',
    status             VARCHAR(16)  NOT NULL DEFAULT 'pending' COMMENT '状态:pending|processing|delivered|rejected',
    quote_amount_cents BIGINT       NULL                     COMMENT '管理员报价金额(分),NULL=未报价;用分存避免小数,展示层 /100 格式化为元',
    custom_skill_name  VARCHAR(128) NULL                     COMMENT '首次交付时写入,关联 custom_skills.name,一工单一技能(Plan 2 写入)',
    reject_reason      VARCHAR(512) NULL                     COMMENT '拒绝原因(status=rejected 时)',
    created_at         DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '提交时间',
    updated_at         DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '最后更新时间(状态变更/评论/交付)',
    PRIMARY KEY (id),
    CONSTRAINT fk_skill_tickets_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_skill_tickets_requester FOREIGN KEY (requester_user_id) REFERENCES users(id),
    KEY idx_skill_tickets_status_updated (status, updated_at DESC),
    KEY idx_skill_tickets_requester (requester_user_id, created_at DESC),
    KEY idx_skill_tickets_org (org_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='skill 定制需求工单,人工处理生命周期';

CREATE TABLE skill_ticket_comments (
    id             CHAR(36)    NOT NULL                 COMMENT '主键 UUID',
    ticket_id      CHAR(36)    NOT NULL                 COMMENT '所属工单',
    author_user_id CHAR(36)    NOT NULL                 COMMENT '发言者 user id(提交者或平台管理员)',
    body           TEXT        NOT NULL                 COMMENT '评论正文',
    created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '发表时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_skill_ticket_comments_ticket FOREIGN KEY (ticket_id) REFERENCES skill_tickets(id),
    CONSTRAINT fk_skill_ticket_comments_author FOREIGN KEY (author_user_id) REFERENCES users(id),
    KEY idx_skill_ticket_comments_ticket (ticket_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='工单多轮对话,提交者与平台管理员沟通需求/反馈';

CREATE TABLE skill_ticket_attachments (
    id          CHAR(36)     NOT NULL                 COMMENT '主键 UUID',
    ticket_id   CHAR(36)     NOT NULL                 COMMENT '所属工单',
    comment_id  CHAR(36)     NULL                     COMMENT '可空,关联具体评论;NULL=随工单初始提交的附件',
    object_path VARCHAR(512) NOT NULL                 COMMENT '对象存储相对路径',
    file_name   VARCHAR(255) NOT NULL                 COMMENT '原始文件名',
    file_size   BIGINT       NOT NULL                 COMMENT '字节大小',
    uploaded_by CHAR(36)     NOT NULL                 COMMENT '上传者 user id',
    created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '上传时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_skill_ticket_attachments_ticket FOREIGN KEY (ticket_id) REFERENCES skill_tickets(id),
    KEY idx_skill_ticket_attachments_ticket (ticket_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='工单附件,提交需求/反馈时上传的示例文档/截图(Plan 2 启用)';

CREATE TABLE custom_skills (
    id          CHAR(36)     NOT NULL                 COMMENT '主键 UUID',
    name        VARCHAR(128) NOT NULL                 COMMENT 'skill 名,等于容器内解压目录名,一工单一 name',
    description TEXT         NOT NULL                 COMMENT '市场卡片展示描述',
    version     VARCHAR(64)  NOT NULL                 COMMENT '版本号,交付时按上传时间自动生成(YYYYMMDD-HHmmss,UTC);唯一即可,"最新"由 created_at 决定',
    tar_path    VARCHAR(512) NOT NULL                 COMMENT '对象存储相对路径 library/custom/<name>/<version>.tar',
    file_size   BIGINT       NOT NULL                 COMMENT 'tar 字节大小',
    file_sha256 CHAR(64)     NOT NULL                 COMMENT 'tar 内容 SHA256',
    ticket_id   CHAR(36)     NOT NULL                 COMMENT '产出该技能的工单',
    created_by  CHAR(36)     NULL                     COMMENT '交付者 user id(平台管理员)',
    created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '交付时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_custom_skills_ticket FOREIGN KEY (ticket_id) REFERENCES skill_tickets(id),
    UNIQUE KEY uk_custom_skills_name_version (name, version),
    KEY idx_custom_skills_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='定制技能归档,多版本共存,工单交付产出(Plan 2 启用)';

CREATE TABLE custom_skill_targets (
    id                CHAR(36)     NOT NULL                COMMENT '主键 UUID',
    custom_skill_name VARCHAR(128) NOT NULL                COMMENT '目标作用的定制技能 name(跨版本)',
    org_id            CHAR(36)     NOT NULL                COMMENT '可见组织',
    audience          VARCHAR(16)  NOT NULL                COMMENT '受众:all_org|org_admins|requester_only',
    created_at        DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_custom_skill_targets_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    UNIQUE KEY uk_custom_skill_targets_name_org (custom_skill_name, org_id),
    KEY idx_custom_skill_targets_org (org_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='定制技能目标可见范围,按 org+受众过滤市场可见性(Plan 2 启用)';
