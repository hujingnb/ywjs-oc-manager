DROP TABLE skill_ticket_messages;

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
