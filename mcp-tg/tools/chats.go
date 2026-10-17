package tools

import (
	"context"

	"scoped-mcp/mcp-tg/telegram"
)

type ListChatsInput struct{}

type ListChatsOutput struct {
	Chats []telegram.Chat `json:"chats"`
}

func (t *Toolset) ListChats(ctx context.Context) (ListChatsOutput, error) {
	chats, err := t.reader.ListChats(ctx, t.allowlist)
	if err == nil {
		return ListChatsOutput{Chats: chats}, nil
	}
	if t.reportRevocation(err) {
		return ListChatsOutput{}, err
	}
	return ListChatsOutput{Chats: t.allowlistAsFallback()}, nil
}

func (t *Toolset) allowlistAsFallback() []telegram.Chat {
	chats := make([]telegram.Chat, 0, len(t.allowlist))
	for _, chat := range t.allowlist {
		chats = append(chats, telegram.Chat{ID: chat.ID, Title: chat.Title, Type: chat.Type})
	}
	return chats
}
