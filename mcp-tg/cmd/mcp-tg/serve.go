package main

import (
	"context"
	"errors"

	"github.com/gotd/td/session"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/guard"
	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/mcp-tg/tools"
)

const version = "0.1.0"

func runServe(ctx context.Context, cfg config.Config, creds config.Credentials, clientSession session.Storage) error {
	if err := cfg.RequireAllowlist(); err != nil {
		return err
	}
	if err := requireSession(ctx, clientSession); err != nil {
		return err
	}
	fromFolders, err := resolveFolderChats(ctx, cfg, creds, clientSession)
	if err != nil {
		return err
	}
	allowlist := config.MergeChats(cfg.Chats, fromFolders)
	tgClient := newTelegramClient(creds, clientSession, guard.New(allowlist))
	return tgClient.Run(ctx, func(ctx context.Context) error {
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "mcp-tg", Version: version}, nil)

		serveCtx, stopOnSessionRevoked := context.WithCancelCause(ctx)
		defer stopOnSessionRevoked(nil)
		onSessionRevoked := func() { stopOnSessionRevoked(telegram.ErrSessionRevoked) }
		tools.New(telegram.NewReader(tgClient.API()), allowlist, onSessionRevoked).Register(mcpServer)

		err := mcpServer.Run(serveCtx, &mcp.StdioTransport{})
		if cause := context.Cause(serveCtx); errors.Is(cause, telegram.ErrSessionRevoked) {
			return cause
		}
		return err
	})
}

func resolveFolderChats(ctx context.Context, cfg config.Config, creds config.Credentials, clientSession session.Storage) ([]config.Chat, error) {
	if len(cfg.Folders) == 0 {
		return nil, nil
	}
	folderClient := newTelegramClient(creds, clientSession, guard.NewFolderReader())
	var fromFolders []config.Chat
	err := folderClient.Run(ctx, func(ctx context.Context) error {
		var err error
		fromFolders, err = telegram.ResolveFolders(ctx, folderClient.API(), cfg.Folders)
		return err
	})
	if err != nil {
		return nil, err
	}
	return fromFolders, nil
}
