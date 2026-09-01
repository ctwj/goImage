package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"hosting/internal/global"

	_ "modernc.org/sqlite"
)

func InitDB() {
	global.DBAvailable = false

	// 获取数据库路径（从配置文件加载，无默认值）
	dbPath := global.AppConfig.Database.Path

	// 验证路径不为空：降级模式（访问不依赖数据库，仅管理功能受限）
	if dbPath == "" {
		log.Println("WARNING: Database path is empty. Running in degraded mode: admin features disabled, image access unaffected.")
		return
	}

	log.Printf("Initializing database at: %s", dbPath)

	var err error
	// 配置 SQLite 数据库：
	// - journal_mode=WAL：启用预写式日志，提供更好的并发性能
	// - synchronous=NORMAL：使用普通同步模式，在性能和安全性之间取得平衡
	global.DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_synchronous=NORMAL")
	if err != nil {
		degrade(fmt.Sprintf("Failed to open database at %s: %v", dbPath, err))
		return
	}

	// 验证数据库连接
	if err = global.DB.Ping(); err != nil {
		global.DB.Close()
		global.DB = nil
		degrade(fmt.Sprintf("Failed to connect to database: %v", err))
		return
	}

	log.Println("Database connection established successfully")

	_, err = global.DB.Exec(`
	CREATE TABLE IF NOT EXISTS images (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		telegram_url TEXT NOT NULL,
		proxy_url TEXT NOT NULL,
		ip_address TEXT NOT NULL,
		user_agent TEXT NOT NULL,
		upload_time DATETIME DEFAULT CURRENT_TIMESTAMP,
		filename TEXT NOT NULL,
		content_type TEXT NOT NULL,
		is_active BOOLEAN DEFAULT 1,
		view_count INTEGER DEFAULT 0,
		file_id TEXT NOT NULL
	)`)

	if err != nil {
		global.DB.Close()
		global.DB = nil
		degrade(fmt.Sprintf("Failed to create images table: %v", err))
		return
	}

	// 创建 documents 表
	_, err = global.DB.Exec(`
	CREATE TABLE IF NOT EXISTS documents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		telegram_url TEXT NOT NULL,
		proxy_url TEXT NOT NULL,
		ip_address TEXT NOT NULL,
		user_agent TEXT NOT NULL,
		upload_time DATETIME DEFAULT CURRENT_TIMESTAMP,
		filename TEXT NOT NULL,
		content_type TEXT NOT NULL,
		is_active BOOLEAN DEFAULT 1,
		view_count INTEGER DEFAULT 0,
		file_id TEXT NOT NULL,
		file_size INTEGER NOT NULL
	)`)

	if err != nil {
		global.DB.Close()
		global.DB = nil
		degrade(fmt.Sprintf("Failed to create documents table: %v", err))
		return
	}

	// 创建优化的索引
	_, err = global.DB.Exec(`
    -- 优化查询时的索引
    CREATE INDEX IF NOT EXISTS idx_proxy_url ON images(proxy_url);
    CREATE INDEX IF NOT EXISTS idx_upload_time ON images(upload_time);
    CREATE INDEX IF NOT EXISTS idx_is_active ON images(is_active);
    CREATE INDEX IF NOT EXISTS idx_file_id ON images(file_id);
    
    -- 复合索引，优化管理页面查询
    CREATE INDEX IF NOT EXISTS idx_active_time ON images(is_active, upload_time DESC);
    
    -- documents 表的索引
    CREATE INDEX IF NOT EXISTS idx_doc_proxy_url ON documents(proxy_url);
    CREATE INDEX IF NOT EXISTS idx_doc_upload_time ON documents(upload_time);
    CREATE INDEX IF NOT EXISTS idx_doc_is_active ON documents(is_active);
    CREATE INDEX IF NOT EXISTS idx_doc_file_id ON documents(file_id);
    CREATE INDEX IF NOT EXISTS idx_doc_active_time ON documents(is_active, upload_time DESC);
    `)

	if err != nil {
		global.DB.Close()
		global.DB = nil
		degrade(fmt.Sprintf("Failed to create indexes: %v", err))
		return
	}

	// 设置数据库连接池参数
	maxOpenConns := 25
	if global.AppConfig.Database.MaxOpenConns > 0 {
		maxOpenConns = global.AppConfig.Database.MaxOpenConns
	}
	global.DB.SetMaxOpenConns(maxOpenConns)

	maxIdleConns := 10
	if global.AppConfig.Database.MaxIdleConns > 0 {
		maxIdleConns = global.AppConfig.Database.MaxIdleConns
	}
	global.DB.SetMaxIdleConns(maxIdleConns)

	connMaxLifetime := 5 * time.Minute
	if global.AppConfig.Database.ConnMaxLifetime != "" {
		if d, err := time.ParseDuration(global.AppConfig.Database.ConnMaxLifetime); err == nil {
			connMaxLifetime = d
		}
	}
	global.DB.SetConnMaxLifetime(connMaxLifetime)

	if err := global.DB.Ping(); err != nil {
		global.DB.Close()
		global.DB = nil
		degrade(fmt.Sprintf("Database connection failed: %v", err))
		return
	}

	global.DBAvailable = true
}

// degrade 输出降级警告。数据库不可用不阻止服务启动：
// 图片访问不依赖数据库，仅管理功能（登记/统计/禁用）受限。
func degrade(reason string) {
	log.Printf("WARNING: %s", reason)
	log.Println("WARNING: Running in degraded mode: admin features disabled, image access unaffected.")
}

// IsAvailable 报告数据库是否可用于可选增强（登记/统计/禁用检查）。
// 调用方在不可用时必须静默跳过数据库操作，不得影响访问结果。
func IsAvailable() bool {
	return global.DBAvailable && global.DB != nil
}

// 数据库操作超时包装函数
func WithDBTimeout(f func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), global.DBTimeout)
	defer cancel()
	return f(ctx)
}
