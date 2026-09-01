package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"hosting/internal/db"
	"hosting/internal/global"
	"hosting/internal/telegram"
	"hosting/internal/utils"
)

// rebuildStats 重建进度统计（contracts/http-api.md §5）
type rebuildStats struct {
	Scanned  int `json:"scanned"`
	Inserted int `json:"inserted"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
}

// HandleRebuild 从频道历史重建管理数据（US4/P3）：
// User API 遍历存储频道历史 → Bot 转发桥接取得 file_id → 去重登记。
// 重建仅恢复管理登记，与图片访问无关；过程可重入（file_id 去重保证幂等）。
func HandleRebuild(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if !db.IsAvailable() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "Database unavailable (degraded mode), rebuild requires a working database",
		})
		return
	}

	if !telegram.IsUserAPIReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "User API not available. Configure and authenticate Telegram User API first (-auth)",
		})
		return
	}

	chatID := global.AppConfig.Telegram.ChatID

	var stats rebuildStats
	var lastErr error

	// 独立 context：重建不受客户端断开影响；整体限时 30 分钟
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	start := time.Now()
	log.Printf("rebuild: started for chat %d", chatID)

	err := telegram.IterateChannelHistory(ctx, chatID, func(item telegram.ChannelHistoryItem) error {
		stats.Scanned++

		if stats.Scanned%50 == 0 {
			log.Printf("rebuild: progress scanned=%d inserted=%d skipped=%d failed=%d",
				stats.Scanned, stats.Inserted, stats.Skipped, stats.Failed)
		}

		if !item.HasPhoto {
			// 纯文本/非 photo 消息（文档等）不在图片重建范围
			return nil
		}

		// Bot 转发桥接：MTProto 消息 → Bot API file_id
		fileID, err := telegram.BridgeFileIDViaForward(chatID, item.MessageID)
		if err != nil {
			stats.Failed++
			log.Printf("rebuild: bridge failed for message %d: %v", item.MessageID, err)
			return nil // 单条失败不中止整体
		}

		// 去重：已登记的 file_id 跳过（幂等，可重入）
		var exists int
		if qerr := db.WithDBTimeout(func(qctx context.Context) error {
			return global.DB.QueryRowContext(qctx,
				"SELECT COUNT(*) FROM images WHERE file_id = ?", fileID).Scan(&exists)
		}); qerr == nil && exists > 0 {
			stats.Skipped++
			return nil
		}

		// 登记信息：caption 作文件名候选，扩展名推断内容类型（访问链路不依赖这些值）
		filename := utils.SanitizeFilename(item.Caption)
		if filename == "" {
			filename = fmt.Sprintf("rebuild_%d", item.MessageID)
		}
		contentType := ""
		if ext := utils.NormalizeFileExtension(filename); ext != "" {
			for mime, e := range global.AllowedMimeTypes {
				if e == ext && filepath.Ext(filename) != "" {
					contentType = mime
					break
				}
			}
		}

		uploadTime := time.Unix(int64(item.Date), 0).Format("2006-01-02 15:04:05")
		ierr := db.WithDBTimeout(func(ictx context.Context) error {
			_, e := global.DB.ExecContext(ictx, `
				INSERT INTO images (
					telegram_url, proxy_url, ip_address, user_agent,
					upload_time, filename, content_type, file_id
				) VALUES ('', ?, 'rebuild', 'rebuild', ?, ?, ?, ?)`,
				utils.BuildFileIDURL(fileID, filename), uploadTime, filename, contentType, fileID)
			return e
		})
		if ierr != nil {
			stats.Failed++
			log.Printf("rebuild: insert failed for message %d (fileID=%s): %v", item.MessageID, fileID, ierr)
			return nil
		}
		stats.Inserted++
		return nil
	})

	if err != nil {
		lastErr = err
		log.Printf("rebuild: aborted: %v", err)
	}

	log.Printf("rebuild: finished in %v, stats=%+v", time.Since(start), stats)

	status := http.StatusOK
	resp := map[string]interface{}{
		"inserted": stats.Inserted,
		"skipped":  stats.Skipped,
		"scanned":  stats.Scanned,
		"failed":   stats.Failed,
	}
	if lastErr != nil {
		// 中途失败：已登记条目保留（可重入续传），按多状态语义返回 207
		status = http.StatusMultiStatus
		resp["error"] = lastErr.Error()
	}
	writeJSON(w, status, resp)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("rebuild: failed to write JSON response: %v", err)
	}
}
