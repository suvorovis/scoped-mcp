package main

import (
	"context"
	"fmt"
	"io"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"gopkg.in/yaml.v3"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
)

const (
	dialogsBatchSize = 100
	yamlIndent       = 2
)

type dialogsDocument struct {
	Chats []config.Chat `yaml:"chats"`
}

func runDialogs(ctx context.Context, creds config.Credentials, clientSession session.Storage, stdout io.Writer) error {
	if err := requireSession(ctx, clientSession); err != nil {
		return err
	}
	tgClient := newTelegramClient(creds, clientSession)
	return tgClient.Run(ctx, func(ctx context.Context) error {
		var entries []config.Chat
		err := dialogs.NewQueryBuilder(tgClient.API()).GetDialogs().BatchSize(dialogsBatchSize).
			ForEach(ctx, func(_ context.Context, elem dialogs.Elem) error {
				if entry, ok := dialogEntry(elem); ok {
					entries = append(entries, entry)
				}
				return nil
			})
		if err != nil {
			return fmt.Errorf("list dialogs: %w", err)
		}
		return writeDialogs(stdout, entries)
	})
}

func dialogEntry(elem dialogs.Elem) (config.Chat, bool) {
	switch p := elem.Peer.(type) {
	case *tg.InputPeerUser:
		entry := config.Chat{ID: p.UserID, Type: config.ChatTypeUser, AccessHash: p.AccessHash}
		if user, ok := elem.Entities.User(p.UserID); ok {
			entry.Title = telegram.UserName(user)
		}
		return entry, true
	case *tg.InputPeerChat:
		entry := config.Chat{ID: p.ChatID, Type: config.ChatTypeChat}
		if chat, ok := elem.Entities.Chat(p.ChatID); ok {
			entry.Title = chat.Title
		}
		return entry, true
	case *tg.InputPeerChannel:
		entry := config.Chat{ID: p.ChannelID, Type: config.ChatTypeChannel, AccessHash: p.AccessHash}
		if channel, ok := elem.Entities.Channel(p.ChannelID); ok {
			entry.Title = channel.Title
		}
		return entry, true
	default:
		return config.Chat{}, false
	}
}

func writeDialogs(w io.Writer, entries []config.Chat) error {
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(yamlIndent)
	if err := encoder.Encode(dialogsDocument{Chats: entries}); err != nil {
		return fmt.Errorf("encode dialogs: %w", err)
	}
	return encoder.Close()
}
