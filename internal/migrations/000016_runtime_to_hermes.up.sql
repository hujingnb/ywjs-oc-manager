-- 运行时切换 OpenClaw → Hermes 之后的字段语义注释更新。
-- 字段类型不变(TEXT),仅 COMMENT。
-- 本地 0 行 bound,无数据迁移负担。

COMMENT ON COLUMN channel_bindings.bound_identity IS
  '微信渠道 iLink Bot 身份,格式 <hex>@im.bot(Hermes runtime 时代)。历史:OpenClaw runtime 时代为 <wxid>@im.wechat。';
