package telegram

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

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

// UploadDocument 使用 User API 上传文档文件
func UploadDocument(ctx context.Context, filePath string, chatID int64, filename string) (string, error) {
	if UserClient == nil {
		return "", fmt.Errorf("User API client not initialized")
	}

	// 获取原始 API 客户端
	api := UserClient.API()

	// 首先获取对话信息以确定类型和 AccessHash
	// 这对于发送到频道/群组是必需的
	log.Printf("Resolving chat ID: %d", chatID)
	
	// 尝试通过 GetDialogs 查找对话
		dialogs, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
			Limit:       100,
			OffsetPeer:  &tg.InputPeerEmpty{}, // 必须提供 offset_peer
		})
		if err != nil {
			log.Printf("Failed to get dialogs: %v", err)
			return "", fmt.Errorf("get dialogs: %w", err)
		}
	
		var peer tg.InputPeerClass
		accessHash := int64(0)
		
		// 从对话列表中查找目标 chatID	// User API 使用正数 ID（例如：2929090067）
	switch d := dialogs.(type) {
	case *tg.MessagesDialogs:
		for _, dialog := range d.Dialogs {
			peerID := dialog.GetPeer()
			switch p := peerID.(type) {
			case *tg.PeerChannel:
				if p.ChannelID == chatID {
					// 查找完整的频道信息
					for _, chat := range d.Chats {
						if ch, ok := chat.(*tg.Channel); ok && ch.ID == chatID {
							accessHash = ch.AccessHash
							peer = &tg.InputPeerChannel{
								ChannelID:  chatID,
								AccessHash: accessHash,
							}
							log.Printf("Found channel: %s", ch.Title)
						}
					}
				}
			case *tg.PeerChat:
				if p.ChatID == chatID {
					peer = &tg.InputPeerChat{
						ChatID: chatID,
					}
					log.Printf("Found chat: ID %d", chatID)
				}
			case *tg.PeerUser:
				if p.UserID == chatID {
					// 查找完整的用户信息
					for _, user := range d.Users {
						if u, ok := user.(*tg.User); ok && u.ID == chatID {
							accessHash = u.AccessHash
							peer = &tg.InputPeerUser{
								UserID:     chatID,
								AccessHash: accessHash,
							}
							log.Printf("Found user: %s (ID: %d, AccessHash: %d)", u.FirstName, chatID, accessHash)
						}
					}
				}
			}
		}
	}

	if peer == nil {
		log.Printf("Chat ID %d not found in dialogs", chatID)
		return "", fmt.Errorf("chat ID %d not found in dialogs", chatID)
	}

	// 创建上传器
	u := uploader.NewUploader(api)

	// 创建消息发送器
	sender := message.NewSender(api).WithUploader(u)

	// 上传文件
	upload, err := u.FromPath(ctx, filePath)
	if err != nil {
		return "", fmt.Errorf("upload file: %w", err)
	}

	// 创建文档消息
	document := message.UploadedDocument(upload, styling.Plain(filename)).
		Filename(filename)

	// 发送文档消息
	result, err := sender.To(peer).Media(ctx, document)
	if err != nil {
		return "", fmt.Errorf("send document: %w", err)
	}

	log.Printf("Document sent successfully, result type: %T", result)

	// 从返回结果中提取 file ID
	// 使用类型断言来检查结果类型
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
		switch u := update.(type) {
		case *tg.UpdateNewMessage:
			if msg, ok := u.Message.(*tg.Message); ok {
				if msg.Media != nil {
					if doc, ok := msg.Media.(*tg.MessageMediaDocument); ok {
						if document, ok := doc.Document.(*tg.Document); ok {
							log.Printf("Document uploaded successfully: %s", filepath.Base(filePath))
							return fmt.Sprintf("document:%d:%d", document.ID, document.AccessHash), nil
						}
					}
				}
			}
		case *tg.UpdateNewChannelMessage:
			if msg, ok := u.Message.(*tg.Message); ok {
				if msg.Media != nil {
					if doc, ok := msg.Media.(*tg.MessageMediaDocument); ok {
						if document, ok := doc.Document.(*tg.Document); ok {
							log.Printf("Document uploaded successfully to channel: %s", filepath.Base(filePath))
							return fmt.Sprintf("document:%d:%d", document.ID, document.AccessHash), nil
						}
					}
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

// DownloadUserFile 从 User API 下载文件
func DownloadUserFile(ctx context.Context, fileID string) ([]byte, error) {
	if UserClient == nil {
		return nil, fmt.Errorf("User API client not initialized")
	}

	api := UserClient.API()

	// 解析 fileID 格式：document:ID:AccessHash
	var docID int64
	var accessHash int64
	_, err := fmt.Sscanf(fileID, "document:%d:%d", &docID, &accessHash)
	if err != nil {
		return nil, fmt.Errorf("invalid fileID format: %s", fileID)
	}

	log.Printf("Downloading document via User API: ID=%d, AccessHash=%d", docID, accessHash)

	// 创建文件位置（不使用 FileReference，尝试简化版本）
	fileLocation := &tg.InputDocumentFileLocation{
		ID:            docID,
		AccessHash:    accessHash,
		FileReference: make([]byte, 0), // 空引用，可能可以工作
		ThumbSize:     "",
	}

	// 下载完整文件（分块下载）
	var content []byte
	offset := int64(0)
	chunkSize := int64(1024 * 1024) // 1MB per chunk

	for {
		fileResult, err := api.UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location:     fileLocation,
			Offset:       offset,
			Limit:        int(chunkSize),
			Precise:      false,
			CDNSupported: false,
		})
		if err != nil {
			return nil, fmt.Errorf("get file at offset %d: %w", offset, err)
		}

		var chunk []byte
		switch f := fileResult.(type) {
		case *tg.UploadFile:
			chunk = f.Bytes
		case *tg.UploadFileCDNRedirect:
			// CDN 重定向，需要额外处理
			return nil, fmt.Errorf("CDN redirect not implemented")
		default:
			return nil, fmt.Errorf("unexpected file result type: %T", fileResult)
		}

		// 如果没有数据，说明下载完成
		if len(chunk) == 0 {
			break
		}

		content = append(content, chunk...)
		log.Printf("Downloaded chunk: offset=%d, size=%d, total=%d", offset, len(chunk), len(content))

		// 如果下载的数据小于请求的大小，说明文件已下载完成
		if int64(len(chunk)) < chunkSize {
			break
		}

		offset += int64(len(chunk))
	}

	log.Printf("Downloading document: %d bytes", len(content))
	return content, nil
}

// GetDownloadURL 从 User API 获取文件的下载 URL
func GetDownloadURL(ctx context.Context, fileID string) (string, error) {
	// 检查 fileID 是否是 User API 格式
	if len(fileID) > 9 && fileID[:9] == "document:" {
		// User API 格式的 fileID，返回特殊标识
		// 下载时需要使用 DownloadUserFile 函数
		return fmt.Sprintf("userapi:%s", fileID), nil
	}
	
	// 对于其他格式，尝试使用 Bot API
	return global.Bot.GetFileDirectURL(fileID)
}