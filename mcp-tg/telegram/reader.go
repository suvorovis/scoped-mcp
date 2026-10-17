package telegram

//go:generate go tool mockgen -destination=mocks/mock_telegram.go -package=mocks scoped-mcp/mcp-tg/telegram API,Reader,FoldersAPI

import (
	"context"

	"github.com/gotd/td/tg"

	"scoped-mcp/mcp-tg/config"
)

type API interface {
	MessagesGetHistory(ctx context.Context, request *tg.MessagesGetHistoryRequest) (tg.MessagesMessagesClass, error)
	MessagesSearch(ctx context.Context, request *tg.MessagesSearchRequest) (tg.MessagesMessagesClass, error)
	MessagesGetReplies(ctx context.Context, request *tg.MessagesGetRepliesRequest) (tg.MessagesMessagesClass, error)
	MessagesGetForumTopics(ctx context.Context, request *tg.MessagesGetForumTopicsRequest) (*tg.MessagesForumTopics, error)
	MessagesGetPeerDialogs(ctx context.Context, peers []tg.InputDialogPeerClass) (*tg.MessagesPeerDialogs, error)
}

var _ API = (*tg.Client)(nil)

type MessagesRequest struct {
	Chat     config.Chat
	TopicID  int
	OffsetID int
	Limit    int
}

type SearchRequest struct {
	Chat     config.Chat
	Query    string
	TopicID  int
	OffsetID int
	Limit    int
}

type Reader interface {
	ListChats(ctx context.Context, chats []config.Chat) ([]Chat, error)
	ListTopics(ctx context.Context, chat config.Chat) ([]Topic, error)
	GetMessages(ctx context.Context, request MessagesRequest) (Page, error)
	SearchMessages(ctx context.Context, request SearchRequest) (Page, error)
}

const topicsLimit = 100

type apiReader struct {
	tgAPI API
}

func NewReader(tgAPI API) Reader {
	return &apiReader{tgAPI: tgAPI}
}

func (r *apiReader) ListChats(ctx context.Context, chats []config.Chat) ([]Chat, error) {
	peers := make([]tg.InputDialogPeerClass, 0, len(chats))
	for _, chat := range chats {
		peers = append(peers, &tg.InputDialogPeer{Peer: InputPeer(chat)})
	}
	result, err := r.tgAPI.MessagesGetPeerDialogs(ctx, peers)
	if err != nil {
		return nil, mapError(err)
	}
	lookup := newNames(result.Users, result.Chats)
	out := make([]Chat, 0, len(chats))
	for _, chat := range chats {
		title, ok := lookup.chatTitle(chat)
		if !ok {
			title = chat.Title
		}
		out = append(out, Chat{ID: chat.ID, Title: title, Type: chat.Type})
	}
	return out, nil
}

func (r *apiReader) ListTopics(ctx context.Context, chat config.Chat) ([]Topic, error) {
	if chat.Type != config.ChatTypeChannel {
		return nil, ErrNotForum
	}
	result, err := r.tgAPI.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{Peer: InputPeer(chat), Limit: topicsLimit})
	if err != nil {
		return nil, mapError(err)
	}
	return topicsFromResult(result.Topics), nil
}

func (r *apiReader) GetMessages(ctx context.Context, request MessagesRequest) (Page, error) {
	peer := InputPeer(request.Chat)
	var (
		result tg.MessagesMessagesClass
		err    error
	)
	if request.TopicID > 0 {
		result, err = r.tgAPI.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
			Peer: peer, MsgID: request.TopicID, OffsetID: request.OffsetID, Limit: request.Limit,
		})
	} else {
		result, err = r.tgAPI.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer: peer, OffsetID: request.OffsetID, Limit: request.Limit,
		})
	}
	if err != nil {
		return Page{}, mapError(err)
	}
	return pageFromResult(result), nil
}

func (r *apiReader) SearchMessages(ctx context.Context, request SearchRequest) (Page, error) {
	result, err := r.tgAPI.MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:     InputPeer(request.Chat),
		Q:        request.Query,
		Filter:   &tg.InputMessagesFilterEmpty{},
		TopMsgID: request.TopicID,
		OffsetID: request.OffsetID,
		Limit:    request.Limit,
	})
	if err != nil {
		return Page{}, mapError(err)
	}
	return pageFromResult(result), nil
}
