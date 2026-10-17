package telegram

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestUserName(t *testing.T) {
	tests := []struct {
		name string
		user *tg.User
		want string
	}{
		{name: "имя и фамилия", user: &tg.User{FirstName: "Ivan", LastName: "Petrov"}, want: "Ivan Petrov"},
		{name: "только имя", user: &tg.User{FirstName: "Ivan"}, want: "Ivan"},
		{name: "только username", user: &tg.User{Username: "ivan"}, want: "ivan"},
		{name: "пусто", user: &tg.User{}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, UserName(tt.user))
		})
	}
}

func TestNamesAuthor(t *testing.T) {
	lookup := newNames(
		[]tg.UserClass{&tg.User{ID: 1, FirstName: "Ivan", LastName: "Petrov"}},
		[]tg.ChatClass{&tg.Chat{ID: 2, Title: "Old group"}, &tg.Channel{ID: 3, Title: "Team alerts"}},
	)
	tests := []struct {
		name   string
		peer   tg.PeerClass
		want   Author
		wantOK bool
	}{
		{name: "user", peer: &tg.PeerUser{UserID: 1}, want: Author{ID: 1, Name: "Ivan Petrov"}, wantOK: true},
		{name: "chat", peer: &tg.PeerChat{ChatID: 2}, want: Author{ID: 2, Name: "Old group"}, wantOK: true},
		{name: "channel", peer: &tg.PeerChannel{ChannelID: 3}, want: Author{ID: 3, Name: "Team alerts"}, wantOK: true},
		{name: "user не в ответе — только id", peer: &tg.PeerUser{UserID: 9}, want: Author{ID: 9}, wantOK: true},
		{name: "nil peer", peer: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := lookup.author(tt.peer)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
