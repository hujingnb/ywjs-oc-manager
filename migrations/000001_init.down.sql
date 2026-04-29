-- 回滚初始迁移时移除当前阶段唯一引入的扩展。
DROP EXTENSION IF EXISTS pgcrypto;
