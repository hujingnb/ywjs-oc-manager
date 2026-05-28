// cmd/migrate 从 OCM_CONFIG 指向的 YAML 读取 database.url；未设置时默认 config/manager.yaml。
// SQL 迁移文件通过 internal/migrations 的 go:embed 打包，并以 iofs source 交给 golang-migrate。
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"oc-manager/internal/config"
	"oc-manager/internal/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("迁移执行失败: %v", err)
	}
}

func run(args []string) error {
	// 迁移命令只接受显式方向，避免默认执行 up/down 带来不可预期的 schema 变更。
	if len(args) != 1 || (args[0] != "up" && args[0] != "down") {
		return errors.New("用法: migrate [up|down]")
	}

	databaseURL, err := loadDatabaseURL()
	if err != nil {
		return err
	}

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("初始化迁移 source 失败: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		src.Close()
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
	}

	return nil
}

func loadDatabaseURL() (string, error) {
	configPath := os.Getenv("OCM_CONFIG")
	if configPath == "" {
		configPath = "config/manager.yaml"
	}
	// 迁移只从 manager 配置读 database.url，不读取 DATABASE_URL，避免本地环境变量误指向其他库。
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return "", err
	}
	return cfg.Database.URL, nil
}
