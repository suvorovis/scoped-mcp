package main

import (
	"github.com/gotd/td/session"
	gotd "github.com/gotd/td/telegram"

	"scoped-mcp/mcp-tg/config"
)

func clientOptions(clientSession session.Storage, middlewares ...gotd.Middleware) gotd.Options {
	return gotd.Options{
		SessionStorage: clientSession,
		Middlewares:    middlewares,
		NoUpdates:      true,
	}
}

func newTelegramClient(creds config.Credentials, clientSession session.Storage, middlewares ...gotd.Middleware) *gotd.Client {
	return gotd.NewClient(creds.APIID, creds.APIHash, clientOptions(clientSession, middlewares...))
}
