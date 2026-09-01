package db

import (
	"path/filepath"
	"testing"

	"hosting/internal/global"
)

// resetDBState 测试后恢复全局状态
func resetDBState(t *testing.T, origPath string) {
	t.Cleanup(func() {
		if global.DB != nil {
			_ = global.DB.Close()
			global.DB = nil
		}
		global.DBAvailable = false
		global.AppConfig.Database.Path = origPath
	})
}

// TestInitDB_DegradedOnEmptyPath 空路径：降级模式，不 Fatal（tasks.md T012）
func TestInitDB_DegradedOnEmptyPath(t *testing.T) {
	orig := global.AppConfig.Database.Path
	resetDBState(t, orig)

	global.AppConfig.Database.Path = ""
	InitDB()

	if IsAvailable() {
		t.Error("空路径应进入降级模式（IsAvailable=false）")
	}
	if global.DB != nil {
		t.Error("降级模式下 global.DB 应为 nil")
	}
}

// TestInitDB_DegradedOnBadPath 坏路径（父目录不存在）：降级模式，不 Fatal
func TestInitDB_DegradedOnBadPath(t *testing.T) {
	orig := global.AppConfig.Database.Path
	resetDBState(t, orig)

	global.AppConfig.Database.Path = filepath.Join(t.TempDir(), "no-such-dir", "broken.db")
	InitDB()

	if IsAvailable() {
		t.Error("坏路径应进入降级模式（IsAvailable=false）")
	}
	if global.DB != nil {
		t.Error("降级模式下 global.DB 应为 nil")
	}
}

// TestInitDB_Success 正常路径：可用且建表生效
func TestInitDB_Success(t *testing.T) {
	// 注意顺序：先取 TempDir 再注册 DB Close 清理（Cleanup 为 LIFO，
	// 确保 Close 先于 TempDir 的 RemoveAll 执行，否则 Windows 下文件占用导致清理失败）
	dbFile := filepath.Join(t.TempDir(), "ok.db")
	orig := global.AppConfig.Database.Path
	resetDBState(t, orig)

	global.AppConfig.Database.Path = dbFile
	InitDB()

	if !IsAvailable() {
		t.Fatal("正常路径应可用（IsAvailable=true）")
	}
	if _, err := global.DB.Exec(
		`INSERT INTO images (telegram_url, proxy_url, ip_address, user_agent, filename, content_type, file_id)
		 VALUES ('u', 'p', 'ip', 'ua', 'f.png', 'image/png', 'fid-test')`); err != nil {
		t.Fatalf("建表后的 images 表应可写: %v", err)
	}
}

// TestIsAvailable_Semantics IsAvailable 在 DB 为 nil 时必须为 false（守卫语义）
func TestIsAvailable_Semantics(t *testing.T) {
	global.DB = nil
	global.DBAvailable = true // 异常状态：标志为 true 但连接为 nil
	if IsAvailable() {
		t.Error("DB 为 nil 时 IsAvailable 必须为 false")
	}
}
