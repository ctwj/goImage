package telegram

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"hosting/internal/global"
)

var (
	UserClient    *telegram.Client
	UserAPIReady  bool
	UserAPIReadyMu sync.RWMutex

	// 优化项2：Dialogs 缓存
	peerCache     = make(map[int64]*PeerCacheEntry)
	peerCacheMu   sync.RWMutex
	peerCacheTTL  = 1 * time.Hour
)

// PeerCacheEntry 缓存条目
type PeerCacheEntry struct {
	Peer       tg.InputPeerClass
	AccessHash int64
	ExpiresAt  time.Time
}

// 优化项1：重试配置
const (
	maxRetries     = 3
	retryBaseDelay = 1 * time.Second
	retryMaxDelay  = 10 * time.Second
)

// InitUserAPI 初始化 Telegram User API
func InitUserAPI() error {
	if global.AppConfig.TelegramUser.APIID == 0 ||
		global.AppConfig.TelegramUser.APIHash == "" {
		log.Println("User API not configured, skipping initialization")
		return nil
	}

	// 配置会话存储
	sessionPath := global.AppConfig.TelegramUser.SessionFile
	if sessionPath == "" {
		sessionPath = "./session.tg"
	}

	// 确保目录存在
	sessionDir := filepath.Dir(sessionPath)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// 检查 session 文件是否存在
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		log.Printf("Session file not found: %s. Please run the program with -auth flag to authenticate.", sessionPath)
		return nil
	}

	// 创建用户客户端
	UserClient = telegram.NewClient(
		global.AppConfig.TelegramUser.APIID,
		global.AppConfig.TelegramUser.APIHash,
		telegram.Options{
			SessionStorage: &session.FileStorage{
				Path: sessionPath,
			},
		},
	)

	// 在后台启动客户端
	go func() {
		log.Printf("Starting User API client with session: %s", sessionPath)
		if err := UserClient.Run(context.Background(), func(ctx context.Context) error {
			// 检查认证状态
			status, err := UserClient.Auth().Status(ctx)
			if err != nil {
				log.Printf("Failed to check auth status: %v", err)
				return fmt.Errorf("failed to check auth status: %w", err)
			}

			if !status.Authorized {
				log.Println("User API not authorized. Session may be invalid or expired. Please run the program with -auth flag to re-authenticate.")
				UserAPIReadyMu.Lock()
				UserAPIReady = false
				UserAPIReadyMu.Unlock()
				return nil
			}

			log.Println("User API connected successfully")

			// 标记 User API 为就绪状态
			UserAPIReadyMu.Lock()
			UserAPIReady = true
			UserAPIReadyMu.Unlock()

			// 保持连接
			<-ctx.Done()

			// 连接断开时标记为未就绪
			UserAPIReadyMu.Lock()
			UserAPIReady = false
			UserAPIReadyMu.Unlock()
			return nil
		}); err != nil {
			log.Printf("User API client error: %v", err)
			UserAPIReadyMu.Lock()
			UserAPIReady = false
			UserAPIReadyMu.Unlock()
		}
	}()

	return nil
}

// AuthenticateUser 执行用户认证（交互式）
func AuthenticateUser() error {
	if global.AppConfig.TelegramUser.APIID == 0 ||
		global.AppConfig.TelegramUser.APIHash == "" {
		return fmt.Errorf("User API not configured. Please set apiId and apiHash in config.json")
	}

	phoneNumber := global.AppConfig.TelegramUser.PhoneNumber
	if phoneNumber == "" {
		return fmt.Errorf("Phone number not configured. Please set phoneNumber in config.json")
	}

	// 配置会话存储
	sessionPath := global.AppConfig.TelegramUser.SessionFile
	if sessionPath == "" {
		sessionPath = "./session.tg"
	}

	// 确保目录存在
	sessionDir := filepath.Dir(sessionPath)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session directory: %w", err)
	}

	// 删除旧的 session 文件（如果存在）
	if _, err := os.Stat(sessionPath); err == nil {
		log.Printf("Removing old session file: %s", sessionPath)
		os.Remove(sessionPath)
	}

	// 创建用户客户端
	client := telegram.NewClient(
		global.AppConfig.TelegramUser.APIID,
		global.AppConfig.TelegramUser.APIHash,
		telegram.Options{
			SessionStorage: &session.FileStorage{
				Path: sessionPath,
			},
		},
	)

	fmt.Println("Starting Telegram User API authentication...")

	// 创建认证器
	authenticator := &codeAuthenticator{
		phoneNumber: phoneNumber,
	}

	// 运行认证流程
	return client.Run(context.Background(), func(ctx context.Context) error {
		// 检查认证状态
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("failed to check auth status: %w", err)
		}

		if status.Authorized {
			fmt.Println("Already authenticated!")
			return nil
		}

		// 执行认证流程 - 使用交互式验证码输入
		if err := client.Auth().IfNecessary(ctx, auth.NewFlow(
			authenticator,
			auth.SendCodeOptions{},
		)); err != nil {
			return fmt.Errorf("authentication failed: %w", err)
		}

		fmt.Println("Authentication successful!")
		fmt.Printf("Session saved to: %s\n", sessionPath)
		fmt.Println("\nYou can now start the server normally.")

		return nil
	})
}

// codeAuthenticator 用于处理验证码输入
type codeAuthenticator struct {
	phoneNumber string
}

func (c *codeAuthenticator) Phone(ctx context.Context) (string, error) {
	return c.phoneNumber, nil
}

func (c *codeAuthenticator) Password(ctx context.Context) (string, error) {
	fmt.Print("Enter 2FA password (if enabled): ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", fmt.Errorf("failed to read password")
}

func (c *codeAuthenticator) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	fmt.Printf("Enter verification code sent to %s: ", c.phoneNumber)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", fmt.Errorf("failed to read code")
}

func (c *codeAuthenticator) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	fmt.Println("Terms of Service accepted")
	return nil
}

func (c *codeAuthenticator) SignUp(ctx context.Context) (auth.UserInfo, error) {
	// 返回空的用户信息，因为我们只做登录，不做注册
	return auth.UserInfo{}, fmt.Errorf("sign up not supported, please use existing phone number")
}

// IsUserAPIReady 检查 User API 是否已就绪
func IsUserAPIReady() bool {
	UserAPIReadyMu.RLock()
	defer UserAPIReadyMu.RUnlock()
	return UserAPIReady
}

// 优化项1：可重试错误判断
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// 网络错误、超时、5xx 错误等可以重试
	errStr := err.Error()
	retryablePatterns := []string{
		"timeout",
		"connection reset",
		"temporary",
		"5xx",
		"500",
		"502",
		"503",
		"504",
		"FLOOD_WAIT",
	}
	for _, pattern := range retryablePatterns {
		if contains(errStr, pattern) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// 优化项2：从缓存获取 peer，如果不存在则解析
func getPeerFromCache(ctx context.Context, api *tg.Client, chatID int64) (tg.InputPeerClass, error) {
	// 先检查缓存
	peerCacheMu.RLock()
	if entry, ok := peerCache[chatID]; ok {
		if time.Now().Before(entry.ExpiresAt) {
			peerCacheMu.RUnlock()
			log.Printf("Peer cache hit for chat ID: %d", chatID)
			return entry.Peer, nil
		}
	}
	peerCacheMu.RUnlock()

	// 缓存未命中，需要解析
	log.Printf("Peer cache miss for chat ID: %d, resolving...", chatID)
	peer, err := resolvePeerFromDialogs(ctx, api, chatID)
	if err != nil {
		return nil, err
	}

	// 存入缓存
	peerCacheMu.Lock()
	peerCache[chatID] = &PeerCacheEntry{
		Peer:      peer,
		ExpiresAt: time.Now().Add(peerCacheTTL),
	}
	peerCacheMu.Unlock()

	return peer, nil
}

// resolvePeerFromDialogs 从对话列表解析 peer
func resolvePeerFromDialogs(ctx context.Context, api *tg.Client, chatID int64) (tg.InputPeerClass, error) {
	dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		Limit:      500,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return nil, fmt.Errorf("get dialogs: %w", err)
	}

	var dialogList []tg.DialogClass
	var chatList []tg.ChatClass
	var userList []tg.UserClass

	switch d := dialogs.(type) {
	case *tg.MessagesDialogs:
		dialogList = d.Dialogs
		chatList = d.Chats
		userList = d.Users
	case *tg.MessagesDialogsSlice:
		dialogList = d.Dialogs
		chatList = d.Chats
		userList = d.Users
		log.Printf("Got MessagesDialogsSlice with %d dialogs", len(dialogList))
	default:
		return nil, fmt.Errorf("unexpected dialogs type: %T", dialogs)
	}

	// 遍历对话查找目标
	for _, dialog := range dialogList {
		peerID := dialog.GetPeer()
		switch p := peerID.(type) {
		case *tg.PeerChannel:
			if p.ChannelID == chatID {
				for _, chat := range chatList {
					if ch, ok := chat.(*tg.Channel); ok && ch.ID == chatID {
						peer := &tg.InputPeerChannel{
							ChannelID:  chatID,
							AccessHash: ch.AccessHash,
						}
						log.Printf("Found channel: %s (ID: %d)", ch.Title, chatID)
						return peer, nil
					}
				}
			}
		case *tg.PeerChat:
			if p.ChatID == chatID {
				peer := &tg.InputPeerChat{ChatID: chatID}
				log.Printf("Found chat: ID %d", chatID)
				return peer, nil
			}
		case *tg.PeerUser:
			if p.UserID == chatID {
				for _, user := range userList {
					if u, ok := user.(*tg.User); ok && u.ID == chatID {
						peer := &tg.InputPeerUser{
							UserID:     chatID,
							AccessHash: u.AccessHash,
						}
						log.Printf("Found user: %s (ID: %d)", u.FirstName, chatID)
						return peer, nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("chat ID %d not found in dialogs", chatID)
}

// UploadDocument 使用 User API 上传文档文件（带重试机制）
func UploadDocument(ctx context.Context, filePath string, chatID int64, filename string) (string, error) {
	if UserClient == nil {
		return "", fmt.Errorf("User API client not initialized")
	}

	api := UserClient.API()

	// 优化项2：使用缓存获取 peer
	peer, err := getPeerFromCache(ctx, api, chatID)
	if err != nil {
		return "", err
	}

	// 优化项1：带重试的上传
	var result interface{}
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * time.Duration(1<<uint(attempt-1))
			if delay > retryMaxDelay {
				delay = retryMaxDelay
			}
			log.Printf("Retrying upload (attempt %d/%d) after %v delay", attempt+1, maxRetries, delay)
			time.Sleep(delay)
		}

		result, lastErr = doUploadDocument(ctx, api, peer, filePath, filename)
		if lastErr == nil {
			// 上传成功，提取 file ID
			fileID, extractErr := extractFileIDFromResult(result, filePath)
			if extractErr != nil {
				return "", extractErr
			}
			return fileID, nil
		}

		// 如果是不可重试的错误，直接返回
		if !isRetryableError(lastErr) {
			return "", lastErr
		}

		log.Printf("Upload attempt %d failed with retryable error: %v", attempt+1, lastErr)
	}

	return "", fmt.Errorf("upload failed after %d retries: %w", maxRetries, lastErr)
}

// doUploadDocument 执行实际上传操作
func doUploadDocument(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, filePath, filename string) (interface{}, error) {
	// 优化：启用多线程并行上传，提升速度 5-10 倍
	// WithThreads(8): 8 个线程并行上传分片
	// WithPartSize(512KB): 每个分片 512KB，平衡效率和开销
	u := uploader.NewUploader(api).
		WithThreads(8).
		WithPartSize(512 * 1024)
	sender := message.NewSender(api).WithUploader(u)

	upload, err := u.FromPath(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("upload file: %w", err)
	}

	document := message.UploadedDocument(upload, styling.Plain(filename)).
		Filename(filename)

	result, err := sender.To(peer).Media(ctx, document)
	if err != nil {
		return nil, fmt.Errorf("send document: %w", err)
	}

	return result, nil
}

// extractFileIDFromResult 从上传结果提取 file ID
func extractFileIDFromResult(result interface{}, filePath string) (string, error) {
	log.Printf("Document sent successfully, result type: %T", result)

	var updates *tg.Updates
	switch r := result.(type) {
	case *tg.Updates:
		updates = r
		log.Printf("Got Updates with %d updates", len(r.Updates))
	case *tg.UpdatesCombined:
		updates = &tg.Updates{
			Updates: r.Updates,
			Users:   r.Users,
			Chats:   r.Chats,
			Date:    r.Date,
			Seq:     r.Seq,
		}
	default:
		return "", fmt.Errorf("unexpected result type: %T", result)
	}

	// 遍历更新，查找消息
	for _, update := range updates.Updates {
		var msg *tg.Message
		switch u := update.(type) {
		case *tg.UpdateNewMessage:
			msg, _ = u.Message.(*tg.Message)
		case *tg.UpdateNewChannelMessage:
			msg, _ = u.Message.(*tg.Message)
		}

		if msg != nil && msg.Media != nil {
			if doc, ok := msg.Media.(*tg.MessageMediaDocument); ok {
				if document, ok := doc.Document.(*tg.Document); ok {
					log.Printf("Document uploaded successfully: %s", filepath.Base(filePath))
					// 优化项4：保存 FileReference
					fileRef := base64.StdEncoding.EncodeToString(document.FileReference)
					return fmt.Sprintf("document:%d:%d:%s", document.ID, document.AccessHash, fileRef), nil
				}
			}
		}
	}

	return "", fmt.Errorf("failed to extract file ID from upload result")
}

// GetUserFileURL 获取 User API 上传的文件 URL
func GetUserFileURL(ctx context.Context, fileID string) (string, error) {
	// 检查是否是 User API 格式的 fileID
	if len(fileID) > 9 && fileID[:9] == "document:" {
		// User API 文件无法通过简单 URL 访问
		// 返回特殊标识，由下载函数处理
		return fmt.Sprintf("userapi:%s", fileID), nil
	}

	// Bot API 文件
	return global.Bot.GetFileDirectURL(fileID)
}

// DocumentStreamReader 流式下载器
type DocumentStreamReader struct {
	api        *tg.Client
	docID      int64
	accessHash int64
	fileRef    []byte
	offset     int64
	chunkSize  int64
	buffer     []byte
	bufPos     int
	eof        bool
	mu         sync.Mutex
}

// NewDocumentStreamReader 创建流式下载器
func NewDocumentStreamReader(api *tg.Client, docID, accessHash int64, fileRef []byte) *DocumentStreamReader {
	return &DocumentStreamReader{
		api:        api,
		docID:      docID,
		accessHash: accessHash,
		fileRef:    fileRef,
		chunkSize:  1024 * 1024, // 1MB per chunk (Telegram API limit)
	}
}

// Read 实现 io.Reader 接口
func (r *DocumentStreamReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 如果缓冲区还有数据，先返回
	if r.bufPos < len(r.buffer) {
		n = copy(p, r.buffer[r.bufPos:])
		r.bufPos += n
		return n, nil
	}

	// 如果已经 EOF，直接返回
	if r.eof {
		return 0, io.EOF
	}

	// 从 Telegram 下载下一个数据块
	chunk, err := r.fetchNextChunk()
	if err != nil {
		return 0, err
	}

	if len(chunk) == 0 {
		r.eof = true
		return 0, io.EOF
	}

	// 复制数据到用户缓冲区
	n = copy(p, chunk)
	if n < len(chunk) {
		// 保存剩余数据到内部缓冲区
		r.buffer = chunk[n:]
		r.bufPos = 0
	} else {
		r.buffer = nil
		r.bufPos = 0
	}

	return n, nil
}

// fetchNextChunk 获取下一个数据块
func (r *DocumentStreamReader) fetchNextChunk() ([]byte, error) {
	fileLocation := &tg.InputDocumentFileLocation{
		ID:            r.docID,
		AccessHash:    r.accessHash,
		FileReference: r.fileRef,
		ThumbSize:     "",
	}

	fileResult, err := r.api.UploadGetFile(context.Background(), &tg.UploadGetFileRequest{
		Location:     fileLocation,
		Offset:       r.offset,
		Limit:        int(r.chunkSize),
		Precise:      false,
		CDNSupported: false,
	})
	if err != nil {
		return nil, fmt.Errorf("get file at offset %d: %w", r.offset, err)
	}

	switch f := fileResult.(type) {
	case *tg.UploadFile:
		r.offset += int64(len(f.Bytes))
		return f.Bytes, nil
	case *tg.UploadFileCDNRedirect:
		return nil, fmt.Errorf("CDN redirect not implemented")
	default:
		return nil, fmt.Errorf("unexpected file result type: %T", fileResult)
	}
}

// Close 实现 io.Closer 接口
func (r *DocumentStreamReader) Close() error {
	return nil
}

// StreamDocument 流式下载文档
func StreamDocument(ctx context.Context, fileID string) (io.ReadCloser, error) {
	if UserClient == nil {
		return nil, fmt.Errorf("User API client not initialized")
	}

	api := UserClient.API()

	// 解析 fileID 格式：document:ID:AccessHash:FileReference
	var docID int64
	var accessHash int64
	var fileRefStr string

	n, err := fmt.Sscanf(fileID, "document:%d:%d:%s", &docID, &accessHash, &fileRefStr)
	if err != nil || n < 2 {
		return nil, fmt.Errorf("invalid fileID format: %s", fileID)
	}

	// 解码 FileReference
	var fileRef []byte
	if n >= 3 && fileRefStr != "" {
		fileRef, err = base64.StdEncoding.DecodeString(fileRefStr)
		if err != nil {
			fileRef = []byte{}
		}
	}

	return NewDocumentStreamReader(api, docID, accessHash, fileRef), nil
}

// DownloadUserFile 从 User API 下载文件（保持向后兼容）
func DownloadUserFile(ctx context.Context, fileID string) ([]byte, error) {
	reader, err := StreamDocument(ctx, fileID)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return io.ReadAll(reader)
}

// GetDownloadURL 从 User API 获取文件的下载 URL
func GetDownloadURL(ctx context.Context, fileID string) (string, error) {
	// 检查 fileID 是否是 User API 格式
	if len(fileID) > 9 && fileID[:9] == "document:" {
		// User API 格式的 fileID，返回特殊标识
		// 下载时需要使用 StreamDocument 或 DownloadUserFile 函数
		return fmt.Sprintf("userapi:%s", fileID), nil
	}

	// 对于其他格式，尝试使用 Bot API
	return global.Bot.GetFileDirectURL(fileID)
}

// ClearPeerCache 清除 peer 缓存（可用于手动刷新）
func ClearPeerCache() {
	peerCacheMu.Lock()
	defer peerCacheMu.Unlock()
	peerCache = make(map[int64]*PeerCacheEntry)
	log.Println("Peer cache cleared")
}

// GetPeerCacheStats 获取缓存统计信息
func GetPeerCacheStats() map[string]interface{} {
	peerCacheMu.RLock()
	defer peerCacheMu.RUnlock()

	return map[string]interface{}{
		"cache_size": len(peerCache),
		"ttl":        peerCacheTTL.String(),
	}
}
