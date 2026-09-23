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
		// 只有 sqlite 分支会填。出错时拿它判断是不是目录写不进去。
		sqliteDir string
	)
	switch cfg.Database.Driver {
	case "sqlite":
		dsn := cfg.Database.DSN
		if dsn == "" {
			dsn = "data/leyun.db"
		}
		if dir := filepath.Dir(dsn); dir != "" && dir != "." {
			// 注意 MkdirAll 对已存在的目录直接返回 nil，不管属主是谁。
			// 所以权限不对这件事在这里发现不了，只能等下面真去开文件时才炸。
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("创建数据库目录失败: %w", err)
			}
			sqliteDir = dir
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
		if hint := notWritableHint(sqliteDir); hint != "" {
			return nil, fmt.Errorf("连接数据库失败: %w（%s）", err, hint)
		}
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

// notWritableHint 判断 SQLite 打不开是不是因为数据目录写不进去，
// 是的话返回一句能照着做的中文提示，否则返回空串。
//
// 为什么值得专门写这么一段：SQLITE_CANTOPEN(14) 被驱动渲染成了
// "unable to open database file: out of memory (14)"。字面看像内存不够，
// 实际八成是权限问题，照着字面排查会彻底跑偏。
//
// 最典型的是容器部署：compose 把宿主机的 ./data 挂到 /app/data，
// 挂载会把宿主机那个目录的属主一起带进来，盖掉镜像里建好的；
// 而宿主机上它通常是 root 建的，容器里却以 uid 1000 在跑。
func notWritableHint(dir string) string {
	if dir == "" {
		return ""
	}
	// 直接试着建个文件——比对着 mode 位算权限可靠，
	// ACL、只读挂载、磁盘满这些情况也一并覆盖到了。
	probe, err := os.CreateTemp(dir, ".leyun-write-probe-*")
	if err == nil {
		probe.Close()
		os.Remove(probe.Name())
		return "" // 写得进去，那就是别的原因，别给错误的引导
	}

	abs, absErr := filepath.Abs(dir)
	if absErr != nil {
		abs = dir
	}
	uid := os.Getuid()
	if uid < 0 { // Windows 上拿不到 uid
		return fmt.Sprintf("数据目录 %s 当前进程写不进去，请检查它的权限", abs)
	}
	return fmt.Sprintf("数据目录 %s 当前用户（uid=%d）写不进去；"+
		"容器部署一般是宿主机上的 data 目录属主不对，在宿主机执行 "+
		"chown -R %d:%d ./data 后重启即可", abs, uid, uid, os.Getgid())
}

type stdWriter struct{}

func (stdWriter) Printf(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}
