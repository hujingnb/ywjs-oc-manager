-- last_error_message 存储 markFailed 时的 cause.Error() 文本，
-- 配合 last_error_status（阶段名）让前端能展示具体失败原因。
-- 重新发起初始化时由应用层在推进状态机前清空。
ALTER TABLE apps
ADD COLUMN last_error_message text NULL;

COMMENT ON COLUMN apps.last_error_message IS
  '上次进入 error 时的错误消息；进入 error 时写入，重新发起对应转移时清空。';
