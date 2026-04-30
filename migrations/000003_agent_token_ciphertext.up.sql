-- B6 阶段：把 agent_token 持久化加密入库，进程重启不再需要 rotate-bootstrap。
-- 字段保持 nullable：旧行（A 阶段注册的节点）继续仅靠内存 cache + agent_token_hash 鉴权。
ALTER TABLE runtime_nodes ADD COLUMN agent_token_ciphertext text NULL;
COMMENT ON COLUMN runtime_nodes.agent_token_ciphertext IS 'agent token 的 AES-256-GCM 密文，base64 编码；nullable 兼容 A 阶段已注册节点。';
