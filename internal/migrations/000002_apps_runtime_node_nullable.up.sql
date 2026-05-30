-- spec-A2a：k8s 编排下 app 不再绑定 runtime_node；放宽 runtime_node_id 为 nullable，
-- 新建 app（k8s）不写该列。真删列与 runtime_nodes 表归 spec-A2b。
ALTER TABLE apps MODIFY COLUMN runtime_node_id CHAR(36) NULL;
