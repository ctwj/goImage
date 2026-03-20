package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"hosting/internal/db"
	"hosting/internal/global"
	"hosting/internal/template"
	"hosting/internal/telegram"
	"hosting/internal/utils"
)

type ImageRecord = global.ImageRecord

type AppError struct {
	Error   error
	Message string
	Code    int
}

func handleError(w http.ResponseWriter, err *AppError) {
	log.Printf("Error: %v", err.Error)
	http.Error(w, err.Message, err.Code)
}

// handleHome 使用 templates/home.html
func HandleHome(w http.ResponseWriter, r *http.Request) {
	tmpl, ok := template.GetTemplate("home")
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	// 检查用户登录状态
	isLoggedIn := false
	session, err := global.Store.Get(r, "admin-session")
	if err == nil {
		if auth, ok := session.Values["authenticated"].(bool); ok && auth {
			isLoggedIn = true
		}
	}

	data := struct {
		Title                 string
		Favicon               string
		MaxFileSize           int
		MaxDocumentSize       int
		RequireLoginForUpload bool
		IsLoggedIn            bool
	}{
		Title:                 utils.GetPageTitle("图床"),
		Favicon:               global.AppConfig.Site.Favicon,
		MaxFileSize:           global.AppConfig.Site.MaxFileSize,
		MaxDocumentSize:       global.AppConfig.Site.MaxDocumentSize,
		RequireLoginForUpload: global.AppConfig.Security.RequireLoginForUpload,
		IsLoggedIn:            isLoggedIn,
	}
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// HandleUpload 处理图片上传
func HandleUpload(w http.ResponseWriter, r *http.Request) {
	// 添加请求追踪ID用于日志
	requestID := uuid.New().String()

	// 使用defer统一处理panic
	defer func() {
		if err := recover(); err != nil {
			log.Printf("[%s] Panic recovered: %v", requestID, err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	// 使用context控制超时
	ctx, cancel := context.WithTimeout(r.Context(), global.UploadTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	// 并发控制使用channel代替mutex
	select {
	case global.UploadSemaphore <- struct{}{}:
		defer func() { <-global.UploadSemaphore }()
	default:
		http.Error(w, "Server is busy", http.StatusServiceUnavailable)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		handleError(w, &AppError{
			Error:   err,
			Message: "无法读取上传文件",
			Code:    http.StatusBadRequest,
		})
		return
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			log.Printf("[%s] failed to close uploaded file: %v", requestID, cerr)
		}
	}()

	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err = file.Seek(0, 0); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	contentType := http.DetectContentType(buffer)
	fileExt, ok := utils.GetFileExtension(contentType)
	if !ok {
		originalExt := utils.NormalizeFileExtension(header.Filename)
		for mime, ext := range global.AllowedMimeTypes {
			if ext == originalExt {
				fileExt = ext
				contentType = mime
				ok = true
				break
			}
		}

		if !ok {
			http.Error(w, "Unsupported file type. Only images and documents are allowed", http.StatusBadRequest)
			return
		}
	}

	// 判断文件类别
	fileCategory := utils.GetFileCategory(contentType)
	log.Printf("[%s] File category determined: %s (content-type: %s)", requestID, fileCategory, contentType)

	// 根据文件类别选择大小限制
	var maxSize int64
	if fileCategory == "image" {
		maxSize = int64(global.AppConfig.Site.MaxFileSize * 1024 * 1024)
	} else {
		maxSize = int64(global.AppConfig.Site.MaxDocumentSize * 1024 * 1024)
	}

	// 验证文件大小
	if header.Size > maxSize {
		maxSizeMB := maxSize / (1024 * 1024)
		http.Error(w, fmt.Sprintf("File size exceeds %dMB limit", maxSizeMB), http.StatusBadRequest)
		return
	}

	ipAddress := utils.ValidateIPAddress(r.RemoteAddr)
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		ipAddress = utils.ValidateIPAddress(forwardedFor)
	}
	userAgent := utils.SanitizeUserAgent(r.Header.Get("User-Agent"))
	filename := utils.SanitizeFilename(header.Filename)

// 创建临时文件
	tempFile, err := os.CreateTemp("", "upload-*"+fileExt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 复制上传文件到临时文件
	_, err = io.Copy(tempFile, file)
	if err != nil {
		tempFile.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	
	// 关闭临时文件以便重命名
	if err := tempFile.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 将临时文件重命名为原始文件名（用于 Telegram 显示）
	finalFilename := utils.CleanFilenameForWindows(filename)
	tempPath := tempFile.Name()
	tempDir := filepath.Dir(tempPath)
	finalPath := filepath.Join(tempDir, finalFilename)
	
	// 删除可能存在的同名文件
	os.Remove(finalPath)
	
	// 重命名临时文件
	if err := os.Rename(tempPath, finalPath); err != nil {
		// 如果重命名失败，尝试使用临时文件名
		log.Printf("[%s] failed to rename temp file to '%s': %v", requestID, finalFilename, err)
		// 尝试使用清理后的文件名
		cleanFilename := utils.CleanFilenameForWindows(filename)
		cleanPath := filepath.Join(tempDir, cleanFilename)
		os.Remove(cleanPath)
		if renameErr := os.Rename(tempPath, cleanPath); renameErr != nil {
			// 如果仍然失败，使用临时文件名
			log.Printf("[%s] failed to rename with cleaned filename: %v, using temp file name", requestID, renameErr)
			finalPath = tempPath
		} else {
			log.Printf("[%s] successfully renamed to cleaned filename: %s", requestID, cleanFilename)
			finalPath = cleanPath
		}
	} else {
		log.Printf("[%s] successfully renamed to: %s", requestID, finalFilename)
	}
	
	// 上传完成后清理文件
	defer func() {
		if err := os.Remove(finalPath); err != nil {
			log.Printf("[%s] failed to remove uploaded file %s: %v", requestID, finalPath, err)
		}
	}()

	var fileID string
	var uploadMethod string

	log.Printf("[%s] Starting upload process, fileCategory: %s", requestID, fileCategory)

	if fileCategory == "image" {
		// 图片文件：使用 Bot API
		uploadMethod = "bot_api"
		log.Printf("[%s] Uploading image: %s (size: %d bytes, content-type: %s)", requestID, filename, header.Size, contentType)
		var message tgbotapi.Message

		// 对于图片文件（JPG/PNG/WebP），使用 NewPhoto 发送
		if contentType == "image/jpeg" || contentType == "image/jpg" || contentType == "image/png" || contentType == "image/webp" {
			photoMsg := tgbotapi.NewPhoto(global.AppConfig.Telegram.ChatID, tgbotapi.FilePath(finalPath))
			message, err = global.Bot.Send(photoMsg)
			if err != nil {
				log.Printf("[%s] Image upload failed: %v", requestID, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// 获取最大尺寸的照片文件ID
			if len(message.Photo) > 0 {
				fileID = message.Photo[len(message.Photo)-1].FileID
				log.Printf("[%s] Image upload succeeded, fileID: %s", requestID, fileID)
			} else {
				log.Printf("[%s] Image upload returned no photo data", requestID)
				http.Error(w, "Image upload failed: no photo data returned", http.StatusInternalServerError)
				return
			}
		}
	} else if fileCategory == "document" {
		// 文档文件：必须使用 User API
		log.Printf("[%s] Uploading document: %s (size: %d bytes, category: %s)", requestID, filename, header.Size, fileCategory)

		// 检查 User API 是否就绪
		if !telegram.IsUserAPIReady() {
			log.Printf("[%s] User API not ready, document upload failed", requestID)
			http.Error(w, "User API not available. Please ensure Telegram User API is properly configured and authenticated.", http.StatusServiceUnavailable)
			return
		}

		uploadMethod = "user_api"
		log.Printf("[%s] User API is ready, attempting upload...", requestID)
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Minute)
		defer cancel()

		fileID, err = telegram.UploadDocument(ctx, finalPath,
			global.AppConfig.TelegramUser.ChatID, filename)

		log.Printf("[%s] UploadDocument returned: fileID=%s, err=%v", requestID, fileID, err)

		if err != nil {
			log.Printf("[%s] User API upload failed: %v", requestID, err)
			http.Error(w, fmt.Sprintf("Document upload failed: %v", err), http.StatusInternalServerError)
			return
		}

		log.Printf("[%s] User API upload succeeded, fileID: %s", requestID, fileID)
	} else {
		log.Printf("[%s] Unknown file category: %s", requestID, fileCategory)
		http.Error(w, fmt.Sprintf("Unknown file category: %s", fileCategory), http.StatusBadRequest)
		return
	}

	// 生成代理 URL（包含原始文件名）
	proxyUUID := uuid.New().String()
	
	// 对文件名进行 URL 编码，确保特殊字符和中文正确处理
	encodedFilename := url.PathEscape(filename)
	var proxyURL string
	if fileCategory == "image" {
		proxyURL = fmt.Sprintf("/file/%s-%s", proxyUUID, encodedFilename)
	} else {
		proxyURL = fmt.Sprintf("/doc/%s-%s", proxyUUID, encodedFilename)
	}

	// 获取 Telegram URL
	var telegramURL string
	if uploadMethod == "bot_api" {
		telegramURL, err = global.Bot.GetFileDirectURL(fileID)
		if err != nil {
			log.Printf("[%s] Failed to get Bot API download URL: %v", requestID, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[%s] Got Bot API download URL: %s", requestID, telegramURL)
	} else {
		// User API 的 URL 获取方式
		// 尝试从 User API 获取下载 URL
		telegramURL, err = telegram.GetDownloadURL(r.Context(), fileID)
		if err != nil {
			log.Printf("[%s] Failed to get User API download URL: %v", requestID, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[%s] Got User API download URL: %s", requestID, telegramURL)
	}

	var scheme string
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	} else {
		scheme = "http"
	}
	fullURL := fmt.Sprintf("%s://%s%s", scheme, r.Host, proxyURL)

	// 根据文件类别选择数据库表
	var tableName string
	if fileCategory == "image" {
		tableName = "images"
	} else {
		tableName = "documents"
	}

	err = db.WithDBTimeout(func(ctx context.Context) error {
		var insertSQL string
		if fileCategory == "image" {
			insertSQL = fmt.Sprintf(`
				INSERT INTO %s (
					telegram_url,
					proxy_url,
					ip_address,
					user_agent,
					filename,
					content_type,
					file_id
				) VALUES (?, ?, ?, ?, ?, ?, ?)
			`, tableName)
		} else {
			insertSQL = fmt.Sprintf(`
				INSERT INTO %s (
					telegram_url,
					proxy_url,
					ip_address,
					user_agent,
					filename,
					content_type,
					file_id,
					file_size
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, tableName)
		}

		stmt, err := global.DB.PrepareContext(ctx, insertSQL)
		if err != nil {
			return err
		}
		defer func() {
			if cerr := stmt.Close(); cerr != nil {
				log.Printf("[%s] failed to close statement: %v", requestID, cerr)
			}
		}()

		if fileCategory == "image" {
			_, err = stmt.ExecContext(ctx,
				telegramURL,
				proxyURL,
				ipAddress,
				userAgent,
				filename,
				contentType,
				fileID,
			)
		} else {
			_, err = stmt.ExecContext(ctx,
				telegramURL,
				proxyURL,
				ipAddress,
				userAgent,
				filename,
				contentType,
				fileID,
				header.Size, // 记录文件大小
			)
		}
		return err
	})

	if err != nil {
		log.Printf("[%s] Database error: %v", requestID, err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	log.Printf("[%s] Successfully saved to database: tableName=%s, fileID=%s, proxyURL=%s", requestID, tableName, fileID, proxyURL)

	t, ok := template.GetTemplate("upload")
	if !ok {
		log.Printf("[%s] Template not found: upload", requestID)
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	data := struct {
		Title    string
		Favicon  string
		URL      string
		Filename string
	}{
		Title:    utils.GetPageTitle("上传"),
		Favicon:  global.AppConfig.Site.Favicon,
		URL:      fullURL,
		Filename: filename,
	}
	if err := t.Execute(w, data); err != nil {
		log.Printf("[%s] Template execution error: %v", requestID, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[%s] Upload completed successfully: filename=%s, proxyURL=%s, uploadMethod=%s", requestID, filename, proxyURL, uploadMethod)
}

func GetTelegramFileURL(fileID string) (string, error) {
	return global.Bot.GetFileDirectURL(fileID)
}

func HandleImage(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pathUUID := vars["uuid"]

	// 从路径中提取真正的 UUID（前 36 个字符）
	// 因为 URL 格式是 /file/{uuid}-{filename}，所以需要提取 UUID 部分
	actualUUID := pathUUID
	if len(pathUUID) > 36 {
		actualUUID = pathUUID[:36]
	}

	// 设置 CORS 头部，允许其他网站嵌入图片
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Range")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")

	// 处理 OPTIONS 预检请求
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000")
	w.Header().Set("Expires", time.Now().AddDate(1, 0, 0).UTC().Format(http.TimeFormat))

	var telegramURL, contentType string
	var isActive bool
	var fileID string
	var currentURL string

	err := db.WithDBTimeout(func(ctx context.Context) error {
		return global.DB.QueryRowContext(ctx, `
            SELECT telegram_url, content_type, is_active, file_id 
            FROM images 
            WHERE proxy_url LIKE ?`,
			fmt.Sprintf("/file/%s%%", actualUUID),
		).Scan(&telegramURL, &contentType, &isActive, &fileID)
	})

	if err != nil {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}

	if !isActive {
		// 尝试读取占位图片
		deletedImage, err := os.ReadFile("static/deleted.jpg")
		if err != nil {
			// 降级处理：占位图片不存在时返回错误
			log.Printf("Failed to read deleted placeholder image: %v", err)
			http.Error(w, "Image has been deleted", http.StatusGone)
			return
		}

		// 设置响应头
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(deletedImage)))
		w.Header().Set("Cache-Control", "public, max-age=86400") // 缓存1天
		w.Header().Set("X-Image-Status", "deleted")              // 标识图片状态

		// 返回占位图片
		w.WriteHeader(http.StatusOK)
		if _, werr := w.Write(deletedImage); werr != nil {
			log.Printf("failed to write deleted placeholder image: %v", werr)
		}

		// 记录访问已删除图片的日志
		log.Printf("Served deleted placeholder for UUID: %s", actualUUID)
		return
	}

	// 检查URL缓存
	global.URLCacheMux.RLock()
	cache, exists := global.URLCache[telegramURL]
	global.URLCacheMux.RUnlock()

	if !exists || time.Now().After(cache.ExpiresAt) {
		// 获取新的URL
		newURL, err := GetTelegramFileURL(fileID)
		if err != nil {
			http.Error(w, "Failed to refresh file URL", http.StatusInternalServerError)
			return
		}

		// 更新缓存
		global.URLCacheMux.Lock()
		global.URLCache[telegramURL] = &global.FileURLCache{
			URL:       newURL,
			ExpiresAt: time.Now().Add(global.URLCacheTime),
		}
		global.URLCacheMux.Unlock()

		currentURL = newURL

		// 更新数据库中的URL
		err = db.WithDBTimeout(func(ctx context.Context) error {
			tx, err := global.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() {
				if err != nil {
					if rerr := tx.Rollback(); rerr != nil {
						log.Printf("failed to rollback transaction: %v", rerr)
					}
				}
			}()

			// 同时更新 telegram_url 和 view_count
			_, err = tx.ExecContext(ctx,
				"UPDATE images SET telegram_url = ?, view_count = view_count + 1 WHERE proxy_url LIKE ?",
				newURL, fmt.Sprintf("/file/%s%%", actualUUID))
			if err != nil {
				return err
			}

			return tx.Commit()
		})

		if err != nil {
			log.Printf("Failed to update database: %v", err)
			// 继续处理请求，不返回错误给用户
		}
	} else {
		currentURL = cache.URL

		// 只更新访问计数
		err = db.WithDBTimeout(func(ctx context.Context) error {
			_, err := global.DB.ExecContext(ctx,
				"UPDATE images SET view_count = view_count + 1 WHERE proxy_url LIKE ?",
				fmt.Sprintf("/file/%s%%", actualUUID))
			return err
		})

		if err != nil {
			log.Printf("Failed to update view count: %v", err)
			// 继续处理请求，不返回错误给用户
		}
	}

	// 创建一个带超时的客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(r.Context(), "GET", currentURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 转发 Range 请求头（支持视频流播放）
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.Printf("failed to close response body: %v", cerr)
		}
	}()

	// 动态检测内容类型，特别是处理Telegram转换GIF为MP4的情况
	actualContentType := contentType

	// 只在非Range请求时进行内容检测，避免影响流播放
	isRangeRequest := r.Header.Get("Range") != ""
	needContentDetection := contentType == "image/gif" && !isRangeRequest

	if needContentDetection {
		// 读取前512字节用于内容类型检测
		peekBuffer := make([]byte, 512)
		n, _ := io.ReadAtLeast(resp.Body, peekBuffer, 512)
		if n == 0 {
			// 如果无法读取足够数据，回退到原始长度
			n, _ = resp.Body.Read(peekBuffer)
		}

		// 检测实际内容类型
		detectedType := http.DetectContentType(peekBuffer[:n])

		// 如果检测到是MP4格式，则使用实际的内容类型
		if detectedType == "video/mp4" {
			actualContentType = "video/mp4"
			log.Printf("GIF file converted to MP4 by Telegram, updating content type")
		}

		// 创建包含原始内容的新reader
		resp.Body = io.NopCloser(io.MultiReader(
			bytes.NewReader(peekBuffer[:n]),
			resp.Body,
		))
	} else if contentType == "image/gif" {
		// 对于Range请求，直接假设是MP4（避免破坏流）
		actualContentType = "video/mp4"
	}

	// 设置响应头 - 必须在 WriteHeader 之前设置所有头部
	w.Header().Set("Content-Type", actualContentType)

	// 如果原始响应有内容长度，也设置它
	if resp.ContentLength > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", resp.ContentLength))
	}

	// 设置响应状态码（如果是 Range 请求则为 206）
	if resp.StatusCode == 206 {
		// 转发 Range 相关的响应头 - 在 WriteHeader 之前
		if contentRange := resp.Header.Get("Content-Range"); contentRange != "" {
			w.Header().Set("Content-Range", contentRange)
		}
		if acceptRanges := resp.Header.Get("Accept-Ranges"); acceptRanges != "" {
			w.Header().Set("Accept-Ranges", acceptRanges)
		}
		w.WriteHeader(206)
	} else {
		// 对于普通请求，声明支持 Range 请求
		w.Header().Set("Accept-Ranges", "bytes")
	}

	// 对于 HEAD 请求，只返回头部信息，不返回文件内容
	if r.Method == "HEAD" {
		return
	}

	// 流式拷贝数据
	buf := make([]byte, 32*1024) // 32KB 缓冲区
	_, err = io.CopyBuffer(w, resp.Body, buf)
	if err != nil {
		log.Printf("Error streaming file: %v", err)
	}
}

// 登录页面使用 templates/login.html
func HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	session, err := global.Store.Get(r, "admin-session")
	if err != nil {
		// 清除旧的 session cookie
		cookie := &http.Cookie{
			Name:     "admin-session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		}
		http.SetCookie(w, cookie)
		// 继续处理登录页面，不返回错误
	}

	if auth, ok := session.Values["authenticated"].(bool); ok && auth {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	t, ok := template.GetTemplate("login")
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}
	data := struct {
		Title   string
		Favicon string
	}{
		Title:   utils.GetPageTitle("登录"),
		Favicon: global.AppConfig.Site.Favicon,
	}
	if err := t.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	session, err := global.Store.Get(r, "admin-session")
	if err != nil {
		// 清除旧的 session cookie
		cookie := &http.Cookie{
			Name:     "admin-session",
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		}
		http.SetCookie(w, cookie)
		// 创建新的 session
		session, err = global.Store.New(r, "admin-session")
		if err != nil {
			http.Error(w, "Failed to create session", http.StatusInternalServerError)
			return
		}
	}

	username := r.FormValue("username")
	if username == global.AppConfig.Admin.Username && r.FormValue("password") == global.AppConfig.Admin.Password {
		session.Values["authenticated"] = true
		err = session.Save(r, w)
		if err != nil {
			log.Printf("Error saving session: %v", err)
			http.Error(w, "Failed to save session", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	http.Error(w, "Invalid credentials", http.StatusUnauthorized)
}

func HandleLogout(w http.ResponseWriter, r *http.Request) {
	session, err := global.Store.Get(r, "admin-session")
	if err != nil {
		log.Printf("Error getting session during logout: %v", err)
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	session.Values["authenticated"] = false
	err = session.Save(r, w)
	if err != nil {
		log.Printf("Error saving session during logout: %v", err)
		http.Error(w, "Failed to save session", http.StatusInternalServerError)
		return
	}

	//log.Println("User logged out successfully")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// 管理页面使用 templates/admin.html
func HandleAdmin(w http.ResponseWriter, r *http.Request) {
	pageSize := 10
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if parsedPage, err := strconv.Atoi(p); err == nil && parsedPage > 0 {
			page = parsedPage
		}
	}
	offset := (page - 1) * pageSize

	// 获取查看类型（image 或 document，默认为 image）
	viewType := r.URL.Query().Get("type")
	if viewType == "" {
		viewType = "image"
	}

	// 根据类型选择表
	var tableName string
	if viewType == "document" {
		tableName = "documents"
	} else {
		tableName = "images"
	}

	// 获取总记录数
	var total int
	err := global.DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&total)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 获取分页数据
	var query string
	if viewType == "document" {
		query = fmt.Sprintf(`
        SELECT id, proxy_url, ip_address, upload_time, filename, is_active, view_count, content_type, file_size
        FROM %s
        ORDER BY upload_time DESC
        LIMIT ? OFFSET ?
    `, tableName)
	} else {
		query = fmt.Sprintf(`
        SELECT id, proxy_url, ip_address, upload_time, filename, is_active, view_count, content_type
        FROM %s
        ORDER BY upload_time DESC
        LIMIT ? OFFSET ?
    `, tableName)
	}

	rows, err := global.DB.Query(query, pageSize, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			log.Printf("failed to close rows: %v", cerr)
		}
	}()

	var images []ImageRecord
	var documents []global.DocumentRecord

	if viewType == "document" {
		for rows.Next() {
			var doc global.DocumentRecord
			err := rows.Scan(&doc.ID, &doc.ProxyURL, &doc.IPAddress, &doc.UploadTime,
				&doc.Filename, &doc.IsActive, &doc.ViewCount, &doc.ContentType, &doc.FileSize)
			if err != nil {
				continue
			}
			documents = append(documents, doc)
		}
	} else {
		for rows.Next() {
			var img ImageRecord
			err := rows.Scan(&img.ID, &img.ProxyURL, &img.IPAddress, &img.UploadTime,
				&img.Filename, &img.IsActive, &img.ViewCount, &img.ContentType)
			if err != nil {
				continue
			}
			images = append(images, img)
		}
	}

	totalPages := (total + pageSize - 1) / pageSize

	t, ok := template.GetTemplate("admin")
	if !ok {
		http.Error(w, "Template not found", http.StatusInternalServerError)
		return
	}

	data := struct {
		Title      string
		Favicon    string
		Images     []ImageRecord
		Documents  []global.DocumentRecord
		ViewType   string
		Page       int
		TotalPages int
		HasPrev    bool
		HasNext    bool
	}{
		Title:      utils.GetPageTitle("管理"),
		Favicon:    global.AppConfig.Site.Favicon,
		Images:     images,
		Documents:  documents,
		ViewType:   viewType,
		Page:       page,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}
	err = t.Execute(w, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func HandleToggleStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	itemType := vars["type"]
	id := vars["id"]

	var tableName string
	if itemType == "image" {
		tableName = "images"
	} else if itemType == "document" {
		tableName = "documents"
	} else {
		http.Error(w, "Invalid item type", http.StatusBadRequest)
		return
	}

	_, err := global.DB.Exec(fmt.Sprintf("UPDATE %s SET is_active = NOT is_active WHERE id = ?", tableName), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// HandleDocument 处理文档文件下载
func HandleDocument(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pathUUID := vars["uuid"]

	// 从路径中提取真正的 UUID（前 36 个字符）
	// 因为 URL 格式是 /doc/{uuid}-{filename}，所以需要提取 UUID 部分
	actualUUID := pathUUID
	if len(pathUUID) > 36 {
		actualUUID = pathUUID[:36]
	}

	// 设置 CORS 头部
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Range")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")

	// 处理 OPTIONS 预检请求
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=31536000")
	w.Header().Set("Expires", time.Now().AddDate(1, 0, 0).UTC().Format(http.TimeFormat))

	var telegramURL, contentType string
	var isActive bool
	var fileID string
	var filename string
	var currentURL string

	err := db.WithDBTimeout(func(ctx context.Context) error {
		return global.DB.QueryRowContext(ctx, `
            SELECT telegram_url, content_type, is_active, file_id, filename
            FROM documents
            WHERE proxy_url LIKE ?`,
			fmt.Sprintf("/doc/%s%%", actualUUID),
		).Scan(&telegramURL, &contentType, &isActive, &fileID, &filename)
	})

	if err != nil {
		http.Error(w, "Document not found", http.StatusNotFound)
		return
	}

	if !isActive {
		// 尝试读取占位图片
		deletedImage, err := os.ReadFile("static/deleted.jpg")
		if err != nil {
			log.Printf("Failed to read deleted placeholder image: %v", err)
			http.Error(w, "Document has been deleted", http.StatusGone)
			return
		}

		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(deletedImage)))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Image-Status", "deleted")

		w.WriteHeader(http.StatusOK)
		if _, werr := w.Write(deletedImage); werr != nil {
			log.Printf("failed to write deleted placeholder image: %v", werr)
		}

		log.Printf("Served deleted placeholder for document UUID: %s", actualUUID)
		return
	}

	// 检查URL缓存
	global.URLCacheMux.RLock()
	cache, exists := global.URLCache[telegramURL]
	global.URLCacheMux.RUnlock()

	// 检查是否是 User API 格式的 fileID
	if len(fileID) > 9 && fileID[:9] == "document:" {
		// User API 文件，直接下载
		log.Printf("Downloading User API file: %s", fileID)
		content, err := telegram.DownloadUserFile(r.Context(), fileID)
		if err != nil {
			log.Printf("Failed to download User API file: %v", err)
			http.Error(w, "Failed to download file", http.StatusInternalServerError)
			return
		}

		// 设置响应头
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

		// 写入文件内容
		if _, err := w.Write(content); err != nil {
			log.Printf("Failed to write User API file content: %v", err)
		}

		log.Printf("Successfully served User API file: %s (%d bytes)", fileID, len(content))
		return
	}

	if !exists || time.Now().After(cache.ExpiresAt) {
		// 获取新的URL (Bot API)
		newURL, err := telegram.GetUserFileURL(r.Context(), fileID)
		if err != nil {
			log.Printf("Failed to get Bot API file URL: %v", err)
			http.Error(w, "Failed to refresh file URL", http.StatusInternalServerError)
			return
		}

		// 检查是否是 User API 文件（不需要缓存）
		if len(newURL) > 8 && newURL[:8] == "userapi:" {
			// User API 文件，直接返回（前面已经处理过了）
			log.Printf("User API file detected, should have been handled earlier")
			http.Error(w, "Internal error: User API file not handled", http.StatusInternalServerError)
			return
		}

		// 更新缓存
		global.URLCacheMux.Lock()
		global.URLCache[telegramURL] = &global.FileURLCache{
			URL:       newURL,
			ExpiresAt: time.Now().Add(global.URLCacheTime),
		}
		global.URLCacheMux.Unlock()
		
		// 设置当前 URL
		currentURL = newURL

		// 更新数据库中的URL
		err = db.WithDBTimeout(func(ctx context.Context) error {
			tx, err := global.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() {
				if err != nil {
					if rerr := tx.Rollback(); rerr != nil {
						log.Printf("failed to rollback transaction: %v", rerr)
					}
				}
			}()

			_, err = tx.ExecContext(ctx,
				"UPDATE documents SET telegram_url = ?, view_count = view_count + 1 WHERE proxy_url LIKE ?",
				newURL, fmt.Sprintf("/doc/%s%%", actualUUID))
			if err != nil {
				return err
			}

			return tx.Commit()
		})

		if err != nil {
			log.Printf("Failed to update database: %v", err)
		}
	} else {
		// 使用缓存的 URL
		currentURL = cache.URL

		// 只更新访问计数
		err = db.WithDBTimeout(func(ctx context.Context) error {
			_, err := global.DB.ExecContext(ctx,
				"UPDATE documents SET view_count = view_count + 1 WHERE proxy_url LIKE ?",
				fmt.Sprintf("/doc/%s%%", actualUUID))
			return err
		})

		if err != nil {
			log.Printf("Failed to update view count: %v", err)
		}
	}

	// 使用 Bot API 的 URL 下载文件
	// 创建一个带超时的客户端
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequestWithContext(r.Context(), "GET", currentURL, nil)
	if err != nil {
		http.Error(w, "Failed to create download request", http.StatusInternalServerError)
		return
	}

	// 添加 User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; GoImage/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to download file", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, "Failed to download file from storage", http.StatusBadGateway)
		return
	}

	// 设置响应头
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", resp.Header.Get("Content-Length"))
	
	// 设置 Content-Disposition，使浏览器使用正确的文件名
	// 从数据库中获取原始文件名
	var originalFilename string
	db.WithDBTimeout(func(ctx context.Context) error {
		return global.DB.QueryRowContext(ctx,
			"SELECT filename FROM documents WHERE proxy_url LIKE ?",
			fmt.Sprintf("/doc/%s%%", actualUUID),
		).Scan(&originalFilename)
	})
	if originalFilename != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", originalFilename))
	}

	// 处理 Range 请求（断点续传）
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		// 解析 Range 请求
		var start int64
		_, err := fmt.Sscanf(rangeHeader, "bytes=%d-", &start)
		if err != nil {
			// 如果解析失败，默认从头开始
			start = 0
		}

		contentLength, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
		
		// 跳过前面的字节
		if start > 0 {
			_, err = io.CopyN(io.Discard, resp.Body, start)
			if err != nil {
				http.Error(w, "Failed to seek in response body", http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, contentLength-1, contentLength))
		w.Header().Set("Content-Length", strconv.FormatInt(contentLength-start, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.WriteHeader(http.StatusPartialContent)

		// 流式拷贝剩余数据
		buf := make([]byte, 32*1024)
		_, err = io.CopyBuffer(w, resp.Body, buf)
		if err != nil {
			log.Printf("Error streaming file: %v", err)
		}
	} else {
		// 对于普通请求，声明支持 Range 请求
		w.Header().Set("Accept-Ranges", "bytes")

		// 对于 HEAD 请求，只返回头部信息，不返回文件内容
		if r.Method == "HEAD" {
			return
		}

		// 流式拷贝数据
		buf := make([]byte, 32*1024)
		_, err = io.CopyBuffer(w, resp.Body, buf)
		if err != nil {
			log.Printf("Error streaming file: %v", err)
		}
	}
}
