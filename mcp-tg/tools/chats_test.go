package tools_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/mcp-tg/telegram/mocks"
	"scoped-mcp/mcp-tg/tools"
)

func TestListChats(t *testing.T) {
	allowlist := []config.Chat{allowedChannel, {ID: 200, Type: config.ChatTypeChat, Title: "group"}}
	fromTelegram := []telegram.Chat{{ID: 300, Title: "Team oncall (live)", Type: config.ChatTypeChannel}, {ID: 200, Title: "group (live)", Type: config.ChatTypeChat}}
	fromConfig := []telegram.Chat{{ID: 300, Title: "Team oncall", Type: config.ChatTypeChannel}, {ID: 200, Title: "group", Type: config.ChatTypeChat}}
	tests := []struct {
		name        string
		reply       []telegram.Chat
		readerErr   error
		want        tools.ListChatsOutput
		wantErr     error
		wantRevoked bool
	}{
		{name: "названия из Telegram", reply: fromTelegram, want: tools.ListChatsOutput{Chats: fromTelegram}},
		{name: "ошибка RPC → названия из конфига без ошибки", readerErr: errors.New("rpc down"), want: tools.ListChatsOutput{Chats: fromConfig}},
		{name: "flood wait → названия из конфига без ошибки", readerErr: &telegram.FloodWaitError{Wait: 1 * time.Second}, want: tools.ListChatsOutput{Chats: fromConfig}},
		{name: "отозванная сессия → ошибка и сигнал", readerErr: telegram.ErrSessionRevoked, wantErr: telegram.ErrSessionRevoked, wantRevoked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := mocks.NewMockReader(gomock.NewController(t))
			reader.EXPECT().ListChats(gomock.Any(), allowlist).Return(tt.reply, tt.readerErr)
			revoked := false

			got, err := tools.New(reader, allowlist, func() { revoked = true }).ListChats(t.Context())

			require.Equal(t, tt.wantRevoked, revoked)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
