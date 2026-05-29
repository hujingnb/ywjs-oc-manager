// Package main 是一次性 platform_admin 种子命令。
//
// 配置：
//   - 从 OCM_CONFIG 指向的 YAML 读取 database.url；未设置时默认 config/manager.yaml。
//
// 用法（容器内）：
//
//	go run ./cmd/seed-admin <username> <password> [display_name]
//
// 操作：
//   - 加载 manager YAML 并校验 database.url 已设置；
//   - 用 auth.HashPassword(Argon2id) 生成 password_hash；
//   - INSERT 一条 role=platform_admin / status=active 的用户行（应用层生成 CHAR(36) 主键）；
//   - 用户名冲突（命中平台管理员用户名唯一索引）时退出 0 不报错（幂等），便于重复执行。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"oc-manager/internal/auth"
	"oc-manager/internal/config"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("用法: seed-admin <username> <password> [display_name]")
	}
	// username/password 来自一次性运维命令参数；只在进程内用于生成 Argon2id hash，不写明文。
	username := os.Args[1]
	password := os.Args[2]
	displayName := username
	if len(os.Args) >= 4 {
		displayName = os.Args[3]
	}
	configPath := os.Getenv("OCM_CONFIG")
	if configPath == "" {
		configPath = "config/manager.yaml"
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Database.URL == "" {
		log.Fatalf("配置文件 %s 缺少 database.url", configPath)
	}
	// go-sql-driver 的 DSN 不接受 mysql:// scheme 前缀，剥离后再交给 sql.Open。
	dsn := strings.TrimPrefix(cfg.Database.URL, "mysql://")
	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer db.Close()

	hash, err := auth.HashPassword(password, auth.DefaultPasswordParams)
	if err != nil {
		log.Fatalf("生成 hash 失败: %v", err)
	}

	// 平台管理员 org_id 为 NULL，命中 uk_users_platform_username 生成列唯一索引时 INSERT IGNORE 静默跳过，
	// 等价于原 PG 的 ON CONFLICT DO NOTHING（幂等）。MySQL :exec 无 RETURNING，用 RowsAffected 判断是否真正插入。
	id := uuid.NewString()
	const insertSQL = `INSERT IGNORE INTO users (id, username, password_hash, display_name, role, status)
VALUES (?, ?, ?, ?, 'platform_admin', 'active')`
	res, err := db.ExecContext(ctx, insertSQL, id, username, hash, displayName)
	if err != nil {
		log.Fatalf("写入用户失败: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fmt.Printf("用户名 %s 已存在，跳过创建\n", username)
		return
	}
	fmt.Printf("已创建 platform_admin: id=%s username=%s\n", id, username)
}
