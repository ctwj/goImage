package telegram

import (
	"context"
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gotd/td/tg"

	"hosting/internal/global"
)

// ChannelHistoryItem 频道历史中的一条消息（重建扫描单元）
type ChannelHistoryItem struct {
	MessageID int
	Date      int    // Unix 秒（tg.Message.Date 为 int）
	Caption   string // 消息文本（图片上传时通常为空）
	HasPhoto  bool   // 是否为 photo 媒体（本特性仅重建图片）
}

// IterateChannelHistory 倒序分页遍历存储频道全部历史消息，逐条回调 fn。
// 回调返回 error 时中止遍历（用于整体失败）；fn 内部的单条错误应自行记录并返回 nil 以继续。
func IterateChannelHistory(ctx context.Context, chatID int64, fn func(item ChannelHistoryItem) error) error {
	if UserClient == nil {
		return fmt.Errorf("User API client not initialized")
	}
	if !IsUserAPIReady() {
		return fmt.Errorf("User API not ready")
	}

	api := UserClient.API()
	peer, err := getPeerFromCache(ctx, api, chatID)
	if err != nil {
		return fmt.Errorf("resolve channel peer: %w", err)
	}

	const limit = 100
	offsetID := 0
	for {
		res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			OffsetID: offsetID,
			Limit:    limit,
		})
		if err != nil {
			return fmt.Errorf("get history (offsetID=%d): %w", offsetID, err)
		}

		msgs, ok := res.(*tg.MessagesChannelMessages)
		if !ok {
			// 频道历史正常返回 MessagesChannelMessages；其他类型（如空对话）视为结束
			log.Printf("rebuild: history returned %T, stopping", res)
			return nil
		}
		if len(msgs.Messages) == 0 {
			return nil
		}

		oldestID := 0
		for _, m := range msgs.Messages {
			msg, ok := m.(*tg.Message)
			if !ok {
				continue
			}
			_, hasPhoto := msg.Media.(*tg.MessageMediaPhoto)
			if err := fn(ChannelHistoryItem{
				MessageID: msg.ID,
				Date:      msg.Date,
				Caption:   msg.Message,
				HasPhoto:  hasPhoto,
			}); err != nil {
				return err
			}
			if oldestID == 0 || msg.ID < oldestID {
				oldestID = msg.ID
			}
		}

		// 不足一页或无法推进时结束
		if len(msgs.Messages) < limit || oldestID == 0 || oldestID == offsetID {
			return nil
		}
		offsetID = oldestID
	}
}

// BridgeFileIDViaForward 将 MTProto 体系的消息转换为 Bot API file_id（research.md D7）：
// 用 Bot 将频道内该消息转发到同一频道 → 从返回的 Message 取得 Bot 体系 file_id → 随即删除转发消息。
func BridgeFileIDViaForward(chatID int64, messageID int) (string, error) {
	if global.Bot == nil {
		return "", fmt.Errorf("bot not initialized")
	}

	msg, err := global.Bot.Send(tgbotapi.NewForward(chatID, chatID, messageID))
	if err != nil {
		return "", fmt.Errorf("forward message %d: %w", messageID, err)
	}
	defer deleteBridgedMessage(chatID, msg.MessageID)

	if len(msg.Photo) > 0 {
		return msg.Photo[len(msg.Photo)-1].FileID, nil
	}
	if msg.Document != nil {
		return msg.Document.FileID, nil
	}
	return "", fmt.Errorf("forwarded message %d contains no media", messageID)
}

// deleteBridgedMessage 清理桥接产生的转发消息（尽力而为）
func deleteBridgedMessage(chatID int64, messageID int) {
	if _, err := global.Bot.Request(tgbotapi.NewDeleteMessage(chatID, messageID)); err != nil {
		log.Printf("rebuild: failed to delete bridged message %d: %v", messageID, err)
	}
}
