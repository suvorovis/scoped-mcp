package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"scoped-mcp/mcp-tg/telegram"
)

const (
	DefaultLimit = 50
	MaxLimit     = 100
)

var ErrInvalidSince = errors.New("tools: since must be an RFC 3339 timestamp")

type GetMessagesInput struct {
	ChatID   int64  `json:"chat_id" jsonschema:"chat id from list_chats"`
	TopicID  int    `json:"topic_id,omitempty" jsonschema:"forum topic id from list_topics"`
	Limit    int    `json:"limit,omitempty" jsonschema:"messages per page, default 50, max 100"`
	BeforeID int    `json:"before_id,omitempty" jsonschema:"return messages with id below this value; pass next_before_id from the previous page"`
	Since    string `json:"since,omitempty" jsonschema:"RFC 3339 timestamp; messages older than it are not returned"`
}

type SearchMessagesInput struct {
	ChatID   int64  `json:"chat_id" jsonschema:"chat id from list_chats"`
	Query    string `json:"query" jsonschema:"search text"`
	TopicID  int    `json:"topic_id,omitempty" jsonschema:"forum topic id from list_topics"`
	Limit    int    `json:"limit,omitempty" jsonschema:"messages per page, default 50, max 100"`
	BeforeID int    `json:"before_id,omitempty" jsonschema:"return messages with id below this value; pass next_before_id from the previous page"`
}

type MessagesOutput struct {
	Messages     []telegram.Message `json:"messages"`
	NextBeforeID int                `json:"next_before_id,omitempty"`
}

func (t *Toolset) GetMessages(ctx context.Context, in GetMessagesInput) (MessagesOutput, error) {
	chat, err := t.allowedChat(in.ChatID)
	if err != nil {
		return MessagesOutput{}, err
	}
	since, err := parseSince(in.Since)
	if err != nil {
		return MessagesOutput{}, err
	}
	limit := clampLimit(in.Limit)
	page, err := t.reader.GetMessages(ctx, telegram.MessagesRequest{
		Chat: chat, TopicID: in.TopicID, OffsetID: in.BeforeID, Limit: limit,
	})
	if err != nil {
		t.reportRevocation(err)
		return MessagesOutput{}, err
	}
	kept, cut := dropOlderThan(page.Messages, since)
	return MessagesOutput{Messages: kept, NextBeforeID: nextBeforeID(page, limit, cut)}, nil
}

func (t *Toolset) SearchMessages(ctx context.Context, in SearchMessagesInput) (MessagesOutput, error) {
	chat, err := t.allowedChat(in.ChatID)
	if err != nil {
		return MessagesOutput{}, err
	}
	limit := clampLimit(in.Limit)
	page, err := t.reader.SearchMessages(ctx, telegram.SearchRequest{
		Chat: chat, Query: in.Query, TopicID: in.TopicID, OffsetID: in.BeforeID, Limit: limit,
	})
	if err != nil {
		t.reportRevocation(err)
		return MessagesOutput{}, err
	}
	return MessagesOutput{Messages: page.Messages, NextBeforeID: nextBeforeID(page, limit, false)}, nil
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}

func parseSince(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	since, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", ErrInvalidSince, raw)
	}
	return since, nil
}

func dropOlderThan(messages []telegram.Message, since time.Time) (kept []telegram.Message, cut bool) {
	kept = make([]telegram.Message, 0, len(messages))
	for _, message := range messages {
		if !message.Date.Before(since) {
			kept = append(kept, message)
		}
	}
	return kept, len(kept) < len(messages)
}

func nextBeforeID(page telegram.Page, limit int, cut bool) int {
	if cut || page.RawCount != limit {
		return 0
	}
	return page.OldestRawID
}
