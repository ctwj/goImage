package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/uuid"

	"hosting/internal/db"
	"hosting/internal/global"
	"hosting/internal/logger"
	"hosting/internal/telegram"
	"hosting/internal/utils"
)

// APIResponse 定义通用API响应结构
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ImageResponse 包含上传后的图片信息
type ImageResponse struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	UploadTime  string `json:"uploadTime"`
}

// HandleAPIUpload 处理通过API上传文件（支持图片和文档）
func HandleAPIUpload(w http.ResponseWriter, r *http.Request) {
	// 设置响应类型为JSON
	w.Header().Set("Content-Type", "application/json")

	// 处理跨域请求
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")

	// 处理预检请求
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 添加请求追踪ID
	requestID := uuid.New().String()
	logger.Debug("开始处理API上传请求: %s", requestID)

	// 使用defer统一处理panic
	defer func() {
		if err := recover(); err != nil {
			logger.Error("[%s] API上传处理中发生panic: %v", requestID, err)
			sendJSONError(w, "服务器内部错误", http.StatusInternalServerError)
		}
	}()

	// 使用context控制超时
	ctx, cancel := context.WithTimeout(r.Context(), global.UploadTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	// 并发控制
	select {
	case global.UploadSemaphore <- struct{}{}:
		defer func() { <-global.UploadSemaphore }()
	default:
		sendJSONError(w, "服务器繁忙，请稍后再试", http.StatusServiceUnavailable)
		return
	}

	// 根据文件类别限制上传文件大小（先使用图片大小限制作为初始值）
	maxSize := int64(global.AppConfig.Site.MaxFileSize * 1024 * 1024)
	r.Body = http.MaxBytesReader(w, r.Body, maxSize)

	// 解析多部分表单
	err := r.ParseMultipartForm(maxSize)
	if err != nil {
		sendJSONError(w, "无法解析表单数据", http.StatusBadRequest)
		return
	}

	// 获取上传文件
	file, header, err := r.FormFile("image")
	if err != nil {
		sendJSONError(w, "无法读取上传文件", http.StatusBadRequest)
		return
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			logger.Error("[%s] failed to close uploaded file: %v", requestID, cerr)
		}
	}()

	// 检测文件类型
	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		sendJSONError(w, "读取文件内容失败", http.StatusInternalServerError)
		return
	}
	if _, err = file.Seek(0, 0); err != nil {
		sendJSONError(w, "读取文件失败", http.StatusInternalServerError)
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
			sendJSONError(w, "不支持的文件类型", http.StatusBadRequest)
			return
		}
	}

	// 判断文件类别
	fileCategory := utils.GetFileCategory(contentType)
	logger.Debug("[%s] File category: %s, content-type: %s", requestID, fileCategory, contentType)

	// 根据文件类别选择大小限制
	if fileCategory == "image" {
		maxSize = int64(global.AppConfig.Site.MaxFileSize * 1024 * 1024)
	} else {
		maxSize = int64(global.AppConfig.Site.MaxDocumentSize * 1024 * 1024)
	}

	// 检查文件大小
	if header.Size > maxSize {
		maxSizeMB := maxSize / (1024 * 1024)
		sendJSONError(w, fmt.Sprintf("文件大小超过限制 (%dMB)", maxSizeMB), http.StatusBadRequest)
		return
	}

	// 记录客户端信息
	ipAddress := utils.ValidateIPAddress(r.RemoteAddr)
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		ipAddress = utils.ValidateIPAddress(forwardedFor)
	}
	userAgent := utils.SanitizeUserAgent(r.Header.Get("User-Agent"))
	filename := utils.SanitizeFilename(header.Filename)

	// 创建临时文件
	tempFile, err := os.CreateTemp("", "upload-*"+fileExt)
	if err != nil {
		sendJSONError(w, "创建临时文件失败", http.StatusInternalServerError)
		return
	}
	tempPath := tempFile.Name()

	// 复制上传文件到临时文件
	_, err = io.Copy(tempFile, file)
	if err != nil {
		tempFile.Close()
		os.Remove(tempPath)
		sendJSONError(w, "保存上传文件失败", http.StatusInternalServerError)
		return
	}
	tempFile.Close()

	// 重命名临时文件为原始文件名（用于 Telegram 显示）
	finalFilename := utils.CleanFilenameForWindows(filename)
	tempDir := filepath.Dir(tempPath)
	finalPath := filepath.Join(tempDir, finalFilename)
	os.Remove(finalPath)

	if err := os.Rename(tempPath, finalPath); err != nil {
		// 重命名失败，使用临时文件路径
		logger.Debug("[%s] Rename failed, using temp path: %v", requestID, err)
		finalPath = tempPath
	}

	// 上传完成后清理文件
	defer func() {
		if err := os.Remove(finalPath); err != nil {
			logger.Debug("[%s] failed to remove temp file: %v", requestID, err)
		}
	}()

	var fileID string
	var telegramURL string
	var proxyURLPath string

	uploadStartTime := time.Now()

	if fileCategory == "image" {
		// 图片文件：使用 Bot API
		logger.Debug("[%s] Uploading image via Bot API: %s", requestID, filename)

		var message tgbotapi.Message
		if contentType == "image/jpeg" || contentType == "image/jpg" || contentType == "image/png" || contentType == "image/webp" {
			photoMsg := tgbotapi.NewPhoto(global.AppConfig.Telegram.ChatID, tgbotapi.FilePath(finalPath))
			message, err = global.Bot.Send(photoMsg)
			if err != nil {
				sendJSONError(w, "上传到存储服务失败", http.StatusInternalServerError)
				return
			}
			if len(message.Photo) > 0 {
				fileID = message.Photo[len(message.Photo)-1].FileID
			}
		} else {
			// GIF 等其他图片格式
			docMsg := tgbotapi.NewDocument(global.AppConfig.Telegram.ChatID, tgbotapi.FilePath(finalPath))
			message, err = global.Bot.Send(docMsg)
			if err != nil {
				sendJSONError(w, "上传到存储服务失败", http.StatusInternalServerError)
				return
			}
			fileID = message.Document.FileID
		}

		telegramURL, err = global.Bot.GetFileDirectURL(fileID)
		if err != nil {
			sendJSONError(w, "获取文件URL失败", http.StatusInternalServerError)
			return
		}

		proxyUUID := uuid.New().String()
		encodedFilename := url.PathEscape(filename)
		proxyURLPath = fmt.Sprintf("/file/%s-%s", proxyUUID, encodedFilename)

	} else if fileCategory == "document" {
		// 文档文件：使用 User API（更快）
		logger.Debug("[%s] Uploading document via User API: %s", requestID, filename)

		if !telegram.IsUserAPIReady() {
			sendJSONError(w, "文档上传服务暂不可用，请联系管理员配置 User API", http.StatusServiceUnavailable)
			return
		}

		uploadCtx, uploadCancel := context.WithTimeout(r.Context(), 60*time.Minute)
		defer uploadCancel()

		fileID, err = telegram.UploadDocument(uploadCtx, finalPath,
			global.AppConfig.TelegramUser.ChatID, filename)
		if err != nil {
			logger.Error("[%s] User API upload failed: %v", requestID, err)
			sendJSONError(w, fmt.Sprintf("文档上传失败: %v", err), http.StatusInternalServerError)
			return
		}

		telegramURL, err = telegram.GetDownloadURL(r.Context(), fileID)
		if err != nil {
			sendJSONError(w, "获取文件URL失败", http.StatusInternalServerError)
			return
		}

		proxyUUID := uuid.New().String()
		encodedFilename := url.PathEscape(filename)
		proxyURLPath = fmt.Sprintf("/doc/%s-%s", proxyUUID, encodedFilename)
	} else {
		sendJSONError(w, "不支持的文件类型", http.StatusBadRequest)
		return
	}

	uploadDuration := time.Since(uploadStartTime)
	logger.Debug("[%s] Upload completed in %v", requestID, uploadDuration)

	// 构建完整URL
	var scheme string
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	} else {
		scheme = "http"
	}
	fullURL := fmt.Sprintf("%s://%s%s", scheme, r.Host, proxyURLPath)

	// 存储记录到数据库
	uploadTime := time.Now().Format(time.RFC3339)

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
					telegram_url, proxy_url, ip_address, user_agent,
					filename, content_type, file_id, upload_time
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, tableName)
		} else {
			insertSQL = fmt.Sprintf(`
				INSERT INTO %s (
					telegram_url, proxy_url, ip_address, user_agent,
					filename, content_type, file_id, file_size, upload_time
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, tableName)
		}

		stmt, err := global.DB.PrepareContext(ctx, insertSQL)
		if err != nil {
			return err
		}
		defer stmt.Close()

		if fileCategory == "image" {
			_, err = stmt.ExecContext(ctx,
				telegramURL, proxyURLPath, ipAddress, userAgent,
				filename, contentType, fileID, uploadTime,
			)
		} else {
			_, err = stmt.ExecContext(ctx,
				telegramURL, proxyURLPath, ipAddress, userAgent,
				filename, contentType, fileID, header.Size, uploadTime,
			)
		}
		return err
	})

	if err != nil {
		logger.Error("[%s] 数据库插入失败: %v", requestID, err)
		sendJSONError(w, "保存记录失败", http.StatusInternalServerError)
		return
	}

	// 返回成功响应
	imageResponse := ImageResponse{
		URL:         fullURL,
		Filename:    filename,
		ContentType: contentType,
		Size:        header.Size,
		UploadTime:  uploadTime,
	}

	response := APIResponse{
		Success: true,
		Message: "上传成功",
		Data:    imageResponse,
	}

	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("[%s] failed to write JSON response: %v", requestID, err)
	}
}

// sendJSONError 发送JSON格式的错误响应
func sendJSONError(w http.ResponseWriter, message string, statusCode int) {
	response := APIResponse{
		Success: false,
		Message: message,
	}

	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("failed to write JSON error response: %v", err)
	}
}

// HandleAPIHealthCheck 健康检查API
func HandleAPIHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	response := APIResponse{
		Success: true,
		Message: "API服务正常",
		Data: map[string]any{
			"version":   "1.0",
			"status":    "operational",
			"timestamp": time.Now().Format(time.RFC3339),
		},
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Error("failed to write health check response: %v", err)
	}
}