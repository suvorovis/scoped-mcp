package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-tg/config"
)

func TestInputPeer(t *testing.T) {
	tests := []struct {
		name string
		chat config.Chat
		want tg.InputPeerClass
	}{
		{name: "user", chat: config.Chat{ID: 1, Type: config.ChatTypeUser, AccessHash: 11}, want: &tg.InputPeerUser{UserID: 1, AccessHash: 11}},
		{name: "chat", chat: config.Chat{ID: 2, Type: config.ChatTypeChat}, want: &tg.InputPeerChat{ChatID: 2}},
		{name: "channel", chat: config.Chat{ID: 3, Type: config.ChatTypeChannel, AccessHash: 33}, want: &tg.InputPeerChannel{ChannelID: 3, AccessHash: 33}},
		{name: "неизвестный тип → empty", chat: config.Chat{ID: 4, Type: "group"}, want: &tg.InputPeerEmpty{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, InputPeer(tt.chat))
		})
	}
}

func TestChatFromInputPeer(t *testing.T) {
	tests := []struct {
		name   string
		peer   tg.InputPeerClass
		want   config.Chat
		wantOK bool
	}{
		{name: "user с access_hash", peer: &tg.InputPeerUser{UserID: 100, AccessHash: 1}, want: config.Chat{ID: 100, Type: config.ChatTypeUser, AccessHash: 1}, wantOK: true},
		{name: "chat", peer: &tg.InputPeerChat{ChatID: 200}, want: config.Chat{ID: 200, Type: config.ChatTypeChat}, wantOK: true},
		{name: "channel с access_hash", peer: &tg.InputPeerChannel{ChannelID: 300, AccessHash: 3}, want: config.Chat{ID: 300, Type: config.ChatTypeChannel, AccessHash: 3}, wantOK: true},
		{name: "self пропускается", peer: &tg.InputPeerSelf{}},
		{name: "empty пропускается", peer: &tg.InputPeerEmpty{}},
		{name: "userFromMessage пропускается", peer: &tg.InputPeerUserFromMessage{Peer: &tg.InputPeerChannel{ChannelID: 300, AccessHash: 3}, MsgID: 1, UserID: 100}},
		{name: "nil пропускается"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ChatFromInputPeer(tt.peer)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
