package telegram

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"

	"hosting/internal/global"
)

var (
	UserClient *telegram.Client
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
		if err := UserClient.Run(context.Background(), func(ctx context.Context) error {
			// 检查认证状态
			status, err := UserClient.Auth().Status(ctx)
			if err != nil {
				return fmt.Errorf("failed to check auth status: %w", err)
			}

			if !status.Authorized {
				log.Println("User API not authorized. Please run the program with -auth flag to authenticate.")
				return nil
			}

			log.Println("User API connected successfully")

			// 保持连接
			<-ctx.Done()
			return nil
		}); err != nil {
			log.Printf("User API error: %v", err)
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
	fmt.Println("Please enter the verification code sent to your phone.")

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

		// 执行认证流程
		// TODO: 实现完整的认证流程
		// 由于 gotd/td 的认证 API 比较复杂，这里提供一个基本框架
		// 用户需要根据 gotd/td 的文档完善这个实现
		return fmt.Errorf("authentication flow not fully implemented. Please implement using gotd/td documentation")

		fmt.Println("Authentication successful!")
		fmt.Printf("Session saved to: %s\n", sessionPath)
		fmt.Println("\nYou can now start the server normally.")

		return nil
	})
}

// UploadDocument 使用 User API 上传文档（占位实现）
func UploadDocument(ctx context.Context, filePath string, chatID int64, filename string) (string, error) {
	// TODO: 实现完整的 User API 文件上传逻辑
	// 由于 gotd/td 的 API 比较复杂，这里提供一个占位实现
	// 用户需要根据 gotd/td 的文档完善这个实现
	return "", fmt.Errorf("User API upload not fully implemented yet. Please implement using gotd/td documentation")
}

// GetUserFileURL 获取 User API 上传的文件 URL（占位实现）
func GetUserFileURL(ctx context.Context, fileID string) (string, error) {
	// TODO: 实现获取 User API 文件 URL 的逻辑
	// 这里需要根据实际需求实现 URL 获取方式
	return fmt.Sprintf("tg://file?file_id=%s", fileID), nil
}

// DownloadUserFile 下载 User API 的文件（占位实现）
func DownloadUserFile(ctx context.Context, fileID, destPath string) error {
	// TODO: 实现下载 User API 文件的逻辑
	// 这里需要根据 gotd/td 的文档实现文件下载
	return fmt.Errorf("not implemented yet")
}