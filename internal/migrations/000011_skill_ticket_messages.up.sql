-- 破坏性替换:统一消息表取代 comments + attachments,不迁移历史数据。
DROP TABLE skill_ticket_attachments;
DROP TABLE skill_ticket_comments;

CREATE TABLE skill_ticket_messages (
    id             CHAR(36)    NOT NULL                 COMMENT '主键 UUID',
    ticket_id      CHAR(36)    NOT NULL                 COMMENT '所属工单',
    author_user_id CHAR(36)    NOT NULL                 COMMENT '发送者 user id(提交者或平台管理员)',
    kind           VARCHAR(16) NOT NULL                 COMMENT '消息类型:text|image|file,决定 body 的 JSON 结构与前端渲染',
    body           JSON        NOT NULL                 COMMENT '消息内容,按 kind 变化:text={"text":".."};image/file={"object_path":"..","file_name":"..","file_size":N,"content_type":".."}',
    created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '发送时间',
    PRIMARY KEY (id),
    CONSTRAINT fk_skill_ticket_messages_ticket FOREIGN KEY (ticket_id) REFERENCES skill_tickets(id),
    CONSTRAINT fk_skill_ticket_messages_author FOREIGN KEY (author_user_id) REFERENCES users(id),
    KEY idx_skill_ticket_messages_ticket (ticket_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='工单统一消息流,kind=text/image/file,body 为按 kind 变化的 JSON,取代原 comments+attachments';
