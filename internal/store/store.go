// Package store 负责数据库连接、自动迁移与首次初始化。
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Yangdongle668/Leyun/internal/config"
	"github.com/Yangdongle668/Leyun/internal/model"
)

// Open 按配置建立数据库连接并执行自动迁移。
func Open(cfg *config.Config) (*gorm.DB, error) {
	gormCfg := &gorm.Config{
		Logger: logger.New(stdWriter{}, logger.Config{
			SlowThreshold:             cfg.Database.SlowThreshold,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		}),
	}

	var (
		db  *gorm.DB
		err error
	)
	switch cfg.Database.Driver {
	case "sqlite":
		dsn := cfg.Database.DSN
		if dsn == "" {
			dsn = "data/leyun.db"
		}
		if dir := filepath.Dir(dsn); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("创建数据库目录失败: %w", err)
			}
		}
		// 开启外键与 WAL，单机部署下并发读写表现更稳。
		if !strings.Contains(dsn, "?") {
			dsn += "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)"
		}
		db, err = gorm.Open(sqlite.Open(dsn), gormCfg)
	case "mysql":
		db, err = gorm.Open(mysql.Open(cfg.Database.DSN), gormCfg)
	case "postgres":
		db, err = gorm.Open(postgres.Open(cfg.Database.DSN), gormCfg)
	default:
		return nil, fmt.Errorf("不支持的数据库驱动: %s", cfg.Database.Driver)
	}
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取数据库连接池失败: %w", err)
	}
	if cfg.Database.Driver == "sqlite" {
		// SQLite 只允许单写者，放开连接数只会把 "database is locked" 摆到运行期。
		//
		// 代价是：事务里绝不能再用 *gorm.DB（连接池）发查询，那会等一个永远不会空出来的
		// 连接，直接把整个进程锁死。业务层因此统一遵循"先校验、后在事务内只写"的写法，
		// 事务内一律使用传入的 tx。
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	} else {
		if cfg.Database.MaxOpenConns > 0 {
			sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
		}
		if cfg.Database.MaxIdleConns > 0 {
			sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
		}
	}
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	return db, nil
}

type stdWriter struct{}

func (stdWriter) Printf(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}
