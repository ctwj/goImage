package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
	_ "modernc.org/sqlite"

	"hosting/internal/global"
)

// pngBytes 最小可识别的 PNG 头部数据（用于内容检测场景）
var pngBytes = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}

// setupImageTest 构造路由与 mock 的 Telegram 远端服务，并替换直链获取注入点
func setupImageTest(t *testing.T, fileBytes []byte, urlErr error) *mux.Router {
	t.Helper()

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if urlErr != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(fileBytes)
	}))
	t.Cleanup(remote.Close)

	orig := GetTelegramFileURL
	GetTelegramFileURL = func(fileID string) (string, error) {
		if urlErr != nil {
			return "", urlErr
		}
		return remote.URL + "/f.bin", nil
	}
	t.Cleanup(func() { GetTelegramFileURL = orig })

	global.URLCacheMux.Lock()
	global.URLCache = make(map[string]*global.FileURLCache)
	global.URLCacheMux.Unlock()

	r := mux.NewRouter()
	r.HandleFunc("/file/{uuid}", HandleImage).Methods("GET", "HEAD", "OPTIONS")
	return r
}

// setupTestDB 建立临时 SQLite 并置为可用态（与 db.go 相同的 images 表结构）
func setupTestDB(t *testing.T) {
	t.Helper()
	dbh, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if _, err := dbh.Exec(`
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
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	global.DB = dbh
	global.DBAvailable = true
	t.Cleanup(func() {
		_ = dbh.Close()
		global.DB = nil
		global.DBAvailable = false
	})
}

// insertTestImage 插入一条图片记录并返回清理函数
func insertTestImage(t *testing.T, fileID string, isActive bool) {
	t.Helper()
	_, err := global.DB.Exec(
		`INSERT INTO images (telegram_url, proxy_url, ip_address, user_agent, filename, content_type, is_active, file_id)
		 VALUES ('tg://ref', '/file/x', '127.0.0.1', 'test', 'a.png', 'image/png', ?, ?)`,
		isActive, fileID)
	if err != nil {
		t.Fatalf("insert test image: %v", err)
	}
}

// TestHandleImage_NoDatabase 数据库不可用时访问行为与正常一致（FR-003 / quickstart 场景 2）
func TestHandleImage_NoDatabase(t *testing.T) {
	global.DB = nil
	global.DBAvailable = false

	r := setupImageTest(t, pngBytes, nil)
	req := httptest.NewRequest("GET", "/file/FILEID123.a.png", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, 期望 200（数据库不可用不得影响访问）, body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, 期望按后缀推断为 image/png", ct)
	}
	if rec.Body.Len() != len(pngBytes) {
		t.Errorf("body 长度 = %d, 期望 %d", rec.Body.Len(), len(pngBytes))
	}
	// CORS 与缓存头契约（contracts/http-api.md §1）
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("缺少 CORS 头 Access-Control-Allow-Origin: *")
	}
	if rec.Header().Get("Cache-Control") != "public, max-age=31536000" {
		t.Errorf("Cache-Control = %q, 期望 public, max-age=31536000", rec.Header().Get("Cache-Control"))
	}
}

// TestHandleImage_DBActive 数据库可用且记录活跃：正常返回且统计累加（FR-007）
func TestHandleImage_DBActive(t *testing.T) {
	setupTestDB(t)
	insertTestImage(t, "FILEID123", true)

	r := setupImageTest(t, pngBytes, nil)
	req := httptest.NewRequest("GET", "/file/FILEID123.a.png", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, 期望 200", rec.Code)
	}
	var viewCount int
	if err := global.DB.QueryRow("SELECT view_count FROM images WHERE file_id = ?", "FILEID123").Scan(&viewCount); err != nil {
		t.Fatalf("query view_count: %v", err)
	}
	if viewCount != 1 {
		t.Errorf("view_count = %d, 期望 1", viewCount)
	}
}

// TestHandleImage_DBDisabled 记录被禁用且数据库可用：返回占位图（FR-007 / 契约§1）
func TestHandleImage_DBDisabled(t *testing.T) {
	setupTestDB(t)
	insertTestImage(t, "FILEID123", false)

	// 占位图夹具（handler 以相对路径 static/deleted.jpg 读取）
	if err := os.MkdirAll("static", 0o755); err != nil {
		t.Fatalf("mkdir static: %v", err)
	}
	placeholder := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG 魔数
	if err := os.WriteFile(filepath.Join("static", "deleted.jpg"), placeholder, 0o644); err != nil {
		t.Fatalf("write placeholder: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll("static") })

	r := setupImageTest(t, pngBytes, nil)
	req := httptest.NewRequest("GET", "/file/FILEID123.a.png", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, 期望 200（占位图）", rec.Code)
	}
	if st := rec.Header().Get("X-Image-Status"); st != "deleted" {
		t.Errorf("X-Image-Status = %q, 期望 deleted", st)
	}
}

// TestHandleImage_DisabledSemanticsLostWithDB 数据库不可用时禁用语义失效：图片照常返回（spec 已声明的取舍）
func TestHandleImage_DisabledSemanticsLostWithDB(t *testing.T) {
	// 记录被禁用，但数据库随后不可用（模拟数据库损坏）——此时应正常返回图片
	global.DB = nil
	global.DBAvailable = false

	r := setupImageTest(t, pngBytes, nil)
	req := httptest.NewRequest("GET", "/file/DISABLEDFID.a.png", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, 期望 200（数据库不可用时禁用语义失效）", rec.Code)
	}
}

// TestHandleImage_InvalidFileID bot 判定 fileId 无效：返回 404 而非 500（FR-005）
func TestHandleImage_InvalidFileID(t *testing.T) {
	global.DB = nil
	global.DBAvailable = false

	r := setupImageTest(t, nil, errors.New("bad file_id"))
	req := httptest.NewRequest("GET", "/file/BADFILEID.a.png", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, 期望 404", rec.Code)
	}
}

// TestHandleImage_UnparsablePath 切分失败（无 "."）：返回 404（契约§6）
func TestHandleImage_UnparsablePath(t *testing.T) {
	global.DB = nil
	global.DBAvailable = false

	r := setupImageTest(t, pngBytes, nil)
	req := httptest.NewRequest("GET", "/file/old-uuid-format-no-dot", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, 期望 404（旧 uuid 格式无法切分 fileId）", rec.Code)
	}
}

// TestHandleImage_OptionAndHead OPTIONS 预检与 HEAD 请求行为（FR-008）
func TestHandleImage_OptionAndHead(t *testing.T) {
	global.DB = nil
	global.DBAvailable = false

	r := setupImageTest(t, pngBytes, nil)

	req := httptest.NewRequest("OPTIONS", "/file/FILEID123.a.png", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("OPTIONS status = %d, 期望 200", rec.Code)
	}

	req2 := httptest.NewRequest("HEAD", "/file/FILEID123.a.png", nil)
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Errorf("HEAD status = %d, 期望 200", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Errorf("HEAD 响应不应包含 body，实际 %d 字节", rec2.Body.Len())
	}
}
