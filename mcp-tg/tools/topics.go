package tools

import (
	"context"

	"scoped-mcp/mcp-tg/telegram"
)

type ListTopicsInput struct {
	ChatID int64 `json:"chat_id" jsonschema:"chat id from list_chats"`
}

type ListTopicsOutput struct {
	Topics []telegram.Topic `json:"topics"`
}

func (t *Toolset) ListTopics(ctx context.Context, in ListTopicsInput) (ListTopicsOutput, error) {
	chat, err := t.allowedChat(in.ChatID)
	if err != nil {
		return ListTopicsOutput{}, err
	}
	topics, err := t.reader.ListTopics(ctx, chat)
	if err != nil {
		t.reportRevocation(err)
		return ListTopicsOutput{}, err
	}
	return ListTopicsOutput{Topics: topics}, nil
}
