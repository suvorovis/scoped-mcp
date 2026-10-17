package main

import (
	"bytes"
	"testing"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-tg/config"
)

func TestDialogEntry(t *testing.T) {
	entities := peer.NewEntities(
		map[int64]*tg.User{100: {ID: 100, FirstName: "Ivan", LastName: "Petrov"}},
		map[int64]*tg.Chat{200: {ID: 200, Title: "Old group"}},
		map[int64]*tg.Channel{300: {ID: 300, Title: "Team oncall"}},
	)
	tests := []struct {
		name   string
		peer   tg.InputPeerClass
		want   config.Chat
		wantOK bool
	}{
		{name: "user", peer: &tg.InputPeerUser{UserID: 100, AccessHash: 11}, want: config.Chat{ID: 100, Type: config.ChatTypeUser, AccessHash: 11, Title: "Ivan Petrov"}, wantOK: true},
		{name: "chat", peer: &tg.InputPeerChat{ChatID: 200}, want: config.Chat{ID: 200, Type: config.ChatTypeChat, Title: "Old group"}, wantOK: true},
		{name: "channel", peer: &tg.InputPeerChannel{ChannelID: 300, AccessHash: 33}, want: config.Chat{ID: 300, Type: config.ChatTypeChannel, AccessHash: 33, Title: "Team oncall"}, wantOK: true},
		{name: "channel без сущности — без title", peer: &tg.InputPeerChannel{ChannelID: 301, AccessHash: 1}, want: config.Chat{ID: 301, Type: config.ChatTypeChannel, AccessHash: 1}, wantOK: true},
		{name: "self пропускается", peer: &tg.InputPeerSelf{}},
		{name: "empty пропускается", peer: &tg.InputPeerEmpty{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := dialogEntry(dialogs.Elem{Peer: tt.peer, Entities: entities})
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestWriteDialogs(t *testing.T) {
	var out bytes.Buffer
	err := writeDialogs(&out, []config.Chat{
		{ID: 300, Type: config.ChatTypeChannel, AccessHash: 33, Title: "Team oncall"},
		{ID: 200, Type: config.ChatTypeChat, Title: "Old group"},
	})
	require.NoError(t, err)
	require.Equal(t, `chats:
  - id: 300
    type: channel
    access_hash: 33
    title: Team oncall
  - id: 200
    type: chat
    title: Old group
`, out.String())
}
