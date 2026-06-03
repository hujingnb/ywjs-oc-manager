CREATE TABLE platform_skills (
    id            CHAR(36)     NOT NULL PRIMARY KEY      COMMENT '主键 UUID',
    name          VARCHAR(128) NOT NULL                 COMMENT 'skill 名，等于容器内解压目录名',
    description   TEXT         NOT NULL                 COMMENT 'skill 描述，市场展示用',
    version       VARCHAR(64)  NOT NULL                 COMMENT '语义版本号',
    tar_path      VARCHAR(512) NOT NULL                 COMMENT '对象存储相对路径 library/platform/<name>/<version>.tar',
    file_size     BIGINT       NOT NULL                 COMMENT 'tar 字节大小',
    file_sha256   CHAR(64)     NOT NULL                 COMMENT 'tar 内容 SHA256，完整性校验',
    metadata_json JSON         NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '附加元数据（作者、标签等）',
    uploaded_by   CHAR(36)     NULL                     COMMENT '上传者 user id（平台管理员）',
    created_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    CONSTRAINT fk_platform_skills_uploaded_by FOREIGN KEY (uploaded_by) REFERENCES users(id),
    UNIQUE KEY uk_platform_skills_name_version (name, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='平台库 skill，平台管理员维护，多版本共存';

CREATE TABLE app_skills (
    id              CHAR(36)     NOT NULL PRIMARY KEY      COMMENT '主键 UUID',
    app_id          CHAR(36)     NOT NULL                 COMMENT '所属实例 app id',
    name            VARCHAR(128) NOT NULL                 COMMENT '解压目录名，实例内唯一，去重键',
    source          VARCHAR(32)  NOT NULL                 COMMENT '来源：platform | clawhub',
    source_ref      VARCHAR(256) NOT NULL                 COMMENT '来源内精准标识：platform=name、clawhub=slug，回源查更新用',
    version         VARCHAR(64)  NOT NULL                 COMMENT '锁定的当前安装版本',
    latest_version  VARCHAR(64)  NULL                     COMMENT '定时任务回源所得最高版本，大于 version 即有更新',
    cached_tar_path VARCHAR(512) NOT NULL                 COMMENT '对象存储缓存路径，恢复走它（确定性 + 抗下架）',
    source_metadata JSON         NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '安装时来源完整元数据快照，后台展示用（抗下架）',
    file_size       BIGINT       NOT NULL                 COMMENT '归档字节大小',
    file_sha256     CHAR(64)     NOT NULL                 COMMENT '归档内容 SHA256',
    installed_by    CHAR(36)     NULL                     COMMENT '安装者 user id',
    installed_at    DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '安装时间',
    last_checked_at DATETIME(6)  NULL                     COMMENT '定时任务上次回源检查时间',
    CONSTRAINT fk_app_skills_app_id FOREIGN KEY (app_id) REFERENCES apps(id),
    UNIQUE KEY uk_app_skills_app_name (app_id, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci
  COMMENT='实例级 skill 安装清单，自包含快照，运行时唯一来源';
