-- published_sites 存每个已发布静态站点（一 slug 一行，反复修改 update-in-place）。
CREATE TABLE published_sites (
    id              CHAR(36)     NOT NULL COMMENT '站点 ID（siteID，UUID）',
    org_id          CHAR(36)     NOT NULL COMMENT '所属企业 ID',
    app_id          CHAR(36)     NOT NULL COMMENT '创建并归属该站点的实例 ID（update-in-place 归属校验）',
    host            VARCHAR(255) NOT NULL COMMENT '完整访问域名 <slug>.<base_domain>（全局唯一=slug 在企业域内唯一）',
    slug            VARCHAR(63)  NOT NULL COMMENT '子域 slug',
    current_version VARCHAR(32)  NOT NULL COMMENT '当前版本标识（如 v1/v2，原子换版指针）',
    s3_prefix       VARCHAR(512) NOT NULL COMMENT '当前版本对象前缀 published-sites/<id>/<version>/（末尾带 /）',
    status          VARCHAR(20)  NOT NULL DEFAULT 'active' COMMENT '状态：active/disabled/expired',
    size_bytes      BIGINT       NOT NULL DEFAULT 0 COMMENT '当前版本总字节数',
    created_at      DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    expires_at      DATETIME(6)  NOT NULL COMMENT '过期时间（每次发布重置为 now + site_ttl_days）',
    updated_at      DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    PRIMARY KEY (id),
    UNIQUE KEY uq_published_sites_host (host),
    KEY idx_published_sites_org_status (org_id, status),
    KEY idx_published_sites_expires (expires_at),
    CONSTRAINT fk_published_sites_org FOREIGN KEY (org_id) REFERENCES organizations(id),
    CONSTRAINT fk_published_sites_app FOREIGN KEY (app_id) REFERENCES apps(id),
    CONSTRAINT published_sites_status_check CHECK (status IN ('active','disabled','expired'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='已发布静态站点';
