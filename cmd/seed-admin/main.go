// Package main 是一次性 platform_admin 种子命令。
//
// 用法（容器内）：
//   go run ./cmd/seed-admin <username> <password> [display_name]
//
// 操作：
//   - 校验 DATABASE_URL 已设置；
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
)

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("用法: seed-admin <username> <password> [display_name]")
	}
	username := os.Args[1]
	password := os.Args[2]
	displayName := username
	if len(os.Args) >= 4 {
		displayName = os.Args[3]
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatalf("缺少 DATABASE_URL")
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
