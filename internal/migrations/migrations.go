// Package migrations 把 SQL 迁移文件以 embed.FS 形式打包进 binary，
// 让 cmd/migrate 不再依赖外部 migrations 目录。
package migrations

import "embed"

// FS 持有 internal/migrations/*.sql 的只读视图；
// 与 golang-migrate/v4 的 iofs source 配合使用。
//
//go:embed *.sql
var FS embed.FS
