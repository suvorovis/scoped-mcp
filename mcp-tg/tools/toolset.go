package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
)

var ErrChatNotAllowed = errors.New("tools: chat is not in the allowlist")

type Toolset struct {
	reader           telegram.Reader
	allowlist        []config.Chat
	onSessionRevoked func()
}

func New(reader telegram.Reader, allowlist []config.Chat, onSessionRevoked func()) *Toolset {
	return &Toolset{reader: reader, allowlist: allowlist, onSessionRevoked: onSessionRevoked}
}

func (t *Toolset) Register(server *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_chats",
		Description: "List Telegram chats available to this server (only chats from the allowlist).",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ ListChatsInput) (*mcp.CallToolResult, ListChatsOutput, error) {
		out, err := t.ListChats(ctx)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_topics",
		Description: "List forum topics of a chat. Fails if the chat is not a forum.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListTopicsInput) (*mcp.CallToolResult, ListTopicsOutput, error) {
		out, err := t.ListTopics(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_messages",
		Description: "Read recent messages of a chat or a forum topic, newest first. Paginate with before_id = next_before_id from the previous page.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetMessagesInput) (*mcp.CallToolResult, MessagesOutput, error) {
		out, err := t.GetMessages(ctx, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_messages",
		Description: "Full-text search inside one chat (optionally inside one forum topic), newest first.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchMessagesInput) (*mcp.CallToolResult, MessagesOutput, error) {
		out, err := t.SearchMessages(ctx, in)
		return nil, out, err
	})
}

func (t *Toolset) allowedChat(chatID int64) (config.Chat, error) {
	for _, chat := range t.allowlist {
		if chat.ID == chatID {
			return chat, nil
		}
	}
	return config.Chat{}, fmt.Errorf("%w: %d", ErrChatNotAllowed, chatID)
}

func (t *Toolset) reportRevocation(err error) (revoked bool) {
	if !errors.Is(err, telegram.ErrSessionRevoked) {
		return false
	}
	if t.onSessionRevoked != nil {
		t.onSessionRevoked()
	}
	return true
}
