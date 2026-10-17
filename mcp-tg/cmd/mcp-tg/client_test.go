package main

import (
	"testing"

	gotd "github.com/gotd/td/telegram"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/guard"
	"scoped-mcp/pkg/secret/mocks"
)

func TestClientOptions(t *testing.T) {
	tests := []struct {
		name            string
		middlewares     []gotd.Middleware
		wantMiddlewares int
	}{
		{name: "без middleware → клиент без middleware"},
		{name: "один middleware → передан клиенту", middlewares: []gotd.Middleware{guard.New([]config.Chat{{ID: 1, Type: config.ChatTypeChat}})}, wantMiddlewares: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secretStore := mocks.NewMockStore(gomock.NewController(t))
			stores := config.SecretStores{SessionStore: secretStore, SessionName: "session"}

			opts := clientOptions(newSessionStorage(stores), tt.middlewares...)

			require.True(t, opts.NoUpdates, "NoUpdates выключает users.getUsers при подключении")
			require.Equal(t, sessionStorage{secretStore: secretStore, secretName: "session"}, opts.SessionStorage)
			require.Len(t, opts.Middlewares, tt.wantMiddlewares)
		})
	}
}
