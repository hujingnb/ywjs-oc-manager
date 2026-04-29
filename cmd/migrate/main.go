package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"oc-manager/internal/config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("迁移执行失败: %v", err)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New("用法: go run ./cmd/migrate [up|down]")
	}

	databaseURL, err := loadDatabaseURL()
	if err != nil {
		return err
	}

	sourceURL, err := migrationSourceURL()
	if err != nil {
		return err
	}

	m, err := migrate.New(sourceURL, databaseURL)
	if err != nil {
		return fmt.Errorf("初始化迁移器失败: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := m.Close()
		if sourceErr != nil {
			log.Printf("关闭迁移 source 失败: %v", sourceErr)
		}
		if databaseErr != nil {
			log.Printf("关闭迁移 database 失败: %v", databaseErr)
		}
	}()

	switch args[0] {
	case "up":
		// 生产环境启动时不自动强制迁移，必须由运维显式执行该命令，避免未审计的 schema 变更随进程启动发生。
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("执行 up 迁移失败: %w", err)
		}
	case "down":
		// down 只回滚一个版本，降低误操作一次性回滚全部迁移的风险。
		if err := m.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("执行 down 迁移失败: %w", err)
		}
	default:
		return fmt.Errorf("未知迁移动作: %s", args[0])
	}

	return nil
}

func loadDatabaseURL() (string, error) {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value, nil
	}

	configPath := os.Getenv("OCM_CONFIG")
	if configPath == "" {
		configPath = "config/config.yaml"
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return "", err
	}
	return cfg.Database.URL, nil
}

func migrationSourceURL() (string, error) {
	dir := os.Getenv("OCM_MIGRATIONS_DIR")
	if dir == "" {
		dir = "migrations"
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("解析迁移目录失败: %w", err)
	}
	return "file://" + filepath.ToSlash(abs), nil
}
