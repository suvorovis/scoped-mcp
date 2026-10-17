package guard

//go:generate go tool mockgen -destination=mocks/mock_invoker.go -package=mocks github.com/gotd/td/tg Invoker

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/bin"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"scoped-mcp/mcp-tg/config"
)

var ErrForbidden = errors.New("guard: forbidden")

type allowlist map[config.ChatKey]struct{}

func New(chats []config.Chat) gotd.MiddlewareFunc {
	allowed := make(allowlist, len(chats))
	for _, chat := range chats {
		allowed[chat.Key()] = struct{}{}
	}
	return func(nextInvoker tg.Invoker) gotd.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			if !allowed.allowsRequest(input) {
				return forbidden(input)
			}
			if err := nextInvoker.Invoke(ctx, input, output); err != nil {
				return err
			}
			if _, isReplies := input.(*tg.MessagesGetRepliesRequest); isReplies && !allowed.allowsReplies(output) {
				discardReplies(output)
				return forbidden(input)
			}
			return nil
		}
	}
}

func (a allowlist) allowsRequest(input bin.Encoder) bool {
	switch request := input.(type) {
	case *tg.HelpGetConfigRequest:
		return true
	case *tg.MessagesGetHistoryRequest:
		return a.allowsPeer(request.Peer)
	case *tg.MessagesSearchRequest:
		return a.allowsPeer(request.Peer)
	case *tg.MessagesGetRepliesRequest:
		return a.allowsPeer(request.Peer)
	case *tg.MessagesGetForumTopicsRequest:
		return a.allowsPeer(request.Peer)
	case *tg.MessagesGetPeerDialogsRequest:
		return len(request.Peers) > 0 && a.allowsDialogPeers(request.Peers)
	default:
		return false
	}
}

func (a allowlist) allowsReplies(output bin.Decoder) bool {
	box, isRepliesBox := output.(*tg.MessagesMessagesBox)
	hasDecodedReplies := isRepliesBox && box.Messages != nil
	if !hasDecodedReplies {
		return false
	}
	return a.allowsMessages(box.Messages)
}

func (a allowlist) allowsMessages(result tg.MessagesMessagesClass) bool {
	modified, isModified := result.AsModified()
	if !isModified {
		return true
	}
	for _, message := range modified.GetMessages() {
		notEmpty, isNotEmpty := message.AsNotEmpty()
		if !isNotEmpty {
			continue
		}
		if !a.allowsMessagePeer(notEmpty.GetPeerID()) {
			return false
		}
	}
	return true
}

func discardReplies(output bin.Decoder) {
	if box, isRepliesBox := output.(*tg.MessagesMessagesBox); isRepliesBox {
		box.Messages = nil
	}
}

func (a allowlist) allowsDialogPeers(peers []tg.InputDialogPeerClass) bool {
	for _, peer := range peers {
		dialogPeer, ok := peer.(*tg.InputDialogPeer)
		if !ok || !a.allowsPeer(dialogPeer.Peer) {
			return false
		}
	}
	return true
}

func (a allowlist) allowsPeer(peer tg.InputPeerClass) bool {
	switch inputPeer := peer.(type) {
	case *tg.InputPeerUser:
		return a.contains(config.ChatTypeUser, inputPeer.UserID)
	case *tg.InputPeerChat:
		return a.contains(config.ChatTypeChat, inputPeer.ChatID)
	case *tg.InputPeerChannel:
		return a.contains(config.ChatTypeChannel, inputPeer.ChannelID)
	default:
		return false
	}
}

func (a allowlist) allowsMessagePeer(peer tg.PeerClass) bool {
	switch messagePeer := peer.(type) {
	case *tg.PeerUser:
		return a.contains(config.ChatTypeUser, messagePeer.UserID)
	case *tg.PeerChat:
		return a.contains(config.ChatTypeChat, messagePeer.ChatID)
	case *tg.PeerChannel:
		return a.contains(config.ChatTypeChannel, messagePeer.ChannelID)
	default:
		return false
	}
}

func (a allowlist) contains(chatType config.ChatType, id int64) bool {
	_, ok := a[config.ChatKey{Type: chatType, ID: id}]
	return ok
}

func forbidden(input bin.Encoder) error {
	return fmt.Errorf("%w: %s", ErrForbidden, typeName(input))
}

func typeName(input bin.Encoder) string {
	if named, ok := input.(interface{ TypeName() string }); ok {
		return named.TypeName()
	}
	return fmt.Sprintf("%T", input)
}
