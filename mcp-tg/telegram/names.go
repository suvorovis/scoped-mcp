package telegram

import (
	"strings"

	"github.com/gotd/td/tg"

	"scoped-mcp/mcp-tg/config"
)

type Author struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
}

func UserName(user *tg.User) string {
	name := strings.TrimSpace(user.FirstName + " " + user.LastName)
	if name == "" {
		return user.Username
	}
	return name
}

type names struct {
	users    map[int64]*tg.User
	chats    map[int64]*tg.Chat
	channels map[int64]*tg.Channel
}

func newNames(users []tg.UserClass, chats []tg.ChatClass) names {
	chatArray := tg.ChatClassArray(chats)
	return names{
		users:    tg.UserClassArray(users).UserToMap(),
		chats:    chatArray.ChatToMap(),
		channels: chatArray.ChannelToMap(),
	}
}

func (n names) author(peer tg.PeerClass) (Author, bool) {
	switch p := peer.(type) {
	case *tg.PeerUser:
		author := Author{ID: p.UserID}
		if user, ok := n.users[p.UserID]; ok {
			author.Name = UserName(user)
		}
		return author, true
	case *tg.PeerChat:
		author := Author{ID: p.ChatID}
		if chat, ok := n.chats[p.ChatID]; ok {
			author.Name = chat.Title
		}
		return author, true
	case *tg.PeerChannel:
		author := Author{ID: p.ChannelID}
		if channel, ok := n.channels[p.ChannelID]; ok {
			author.Name = channel.Title
		}
		return author, true
	default:
		return Author{}, false
	}
}

func (n names) title(peer tg.PeerClass) (string, bool) {
	author, ok := n.author(peer)
	if !ok || author.Name == "" {
		return "", false
	}
	return author.Name, true
}

func (n names) chatTitle(chat config.Chat) (string, bool) {
	return n.title(peerFor(chat))
}
