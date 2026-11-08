package telegram

import (
	"bytes"
	"context"
	"errors"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/xonoko/infra-monitor/internal/channel"
)

var _ channel.FileSender = (*Channel)(nil)

// SendFile attaches a document (e.g. a .diff) as a reply in the incident thread.
func (c *Channel) SendFile(ctx context.Context, thread channel.ThreadRef, filename string, content []byte, caption string) (channel.MessageRef, error) {
	chatID := c.chatID(thread.ChatID)
	replyTo, _ := strconv.Atoi(thread.ThreadID)
	p := &bot.SendDocumentParams{
		ChatID:    chatID,
		Document:  &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(content)},
		Caption:   fit(RenderMarkdown(caption), ""),
		ParseMode: models.ParseModeHTML,
	}
	if len(p.Caption) > 1000 {
		p.Caption = p.Caption[:1000]
	}
	if replyTo > 0 {
		p.ReplyParameters = &models.ReplyParameters{MessageID: replyTo, AllowSendingWithoutReply: true}
	}
	var msg *models.Message
	err := c.call(ctx, "sendDocument", chatID, true, func(ctx context.Context) error {
		var err error
		msg, err = c.bot.SendDocument(ctx, p)
		return err
	})
	if err != nil {
		return channel.MessageRef{}, err
	}
	if msg == nil {
		return channel.MessageRef{}, errors.New("telegram: empty sendDocument result")
	}
	return channel.MessageRef{ChatID: strconv.FormatInt(msg.Chat.ID, 10), MessageID: strconv.Itoa(msg.ID)}, nil
}
