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
//   - INSERT 一条 role=platform_admin / status=active 的用户行；
//   - 用户名冲突时退出 0 不报错（幂等），便于重复执行。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

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
	dsn := cfg.Database.URL
	if dsn == "" {
		log.Fatalf("配置文件 %s 缺少 database.url", configPath)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	defer conn.Close(ctx)

	hash, err := auth.HashPassword(password, auth.DefaultPasswordParams)
	if err != nil {
		log.Fatalf("生成 hash 失败: %v", err)
	}

	const insertSQL = `
INSERT INTO users (username, password_hash, display_name, role, status)
VALUES ($1, $2, $3, 'platform_admin', 'active')
ON CONFLICT (username) DO NOTHING
RETURNING id`
	var id string
	err = conn.QueryRow(ctx, insertSQL, username, hash, displayName).Scan(&id)
	if err == pgx.ErrNoRows {
		fmt.Printf("用户名 %s 已存在，跳过创建\n", username)
		return
	}
	if err != nil {
		log.Fatalf("写入用户失败: %v", err)
	}
	fmt.Printf("已创建 platform_admin: id=%s username=%s\n", strings.TrimSpace(id), username)
}
