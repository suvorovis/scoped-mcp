package telegram

import (
	"github.com/gotd/td/tg"

	"scoped-mcp/mcp-tg/config"
)

func InputPeer(chat config.Chat) tg.InputPeerClass {
	switch chat.Type {
	case config.ChatTypeUser:
		return &tg.InputPeerUser{UserID: chat.ID, AccessHash: chat.AccessHash}
	case config.ChatTypeChat:
		return &tg.InputPeerChat{ChatID: chat.ID}
	case config.ChatTypeChannel:
		return &tg.InputPeerChannel{ChannelID: chat.ID, AccessHash: chat.AccessHash}
	default:
		return &tg.InputPeerEmpty{}
	}
}

func ChatFromInputPeer(peer tg.InputPeerClass) (config.Chat, bool) {
	switch inputPeer := peer.(type) {
	case *tg.InputPeerUser:
		return config.Chat{ID: inputPeer.UserID, Type: config.ChatTypeUser, AccessHash: inputPeer.AccessHash}, true
	case *tg.InputPeerChat:
		return config.Chat{ID: inputPeer.ChatID, Type: config.ChatTypeChat}, true
	case *tg.InputPeerChannel:
		return config.Chat{ID: inputPeer.ChannelID, Type: config.ChatTypeChannel, AccessHash: inputPeer.AccessHash}, true
	default:
		return config.Chat{}, false
	}
}

func peerFor(chat config.Chat) tg.PeerClass {
	switch chat.Type {
	case config.ChatTypeUser:
		return &tg.PeerUser{UserID: chat.ID}
	case config.ChatTypeChat:
		return &tg.PeerChat{ChatID: chat.ID}
	case config.ChatTypeChannel:
		return &tg.PeerChannel{ChannelID: chat.ID}
	default:
		return nil
	}
}
