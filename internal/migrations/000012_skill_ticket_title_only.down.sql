ALTER TABLE skill_tickets
    ADD COLUMN description TEXT NULL COMMENT '旧版需求描述字段,新版需求内容写入 skill_ticket_messages';
