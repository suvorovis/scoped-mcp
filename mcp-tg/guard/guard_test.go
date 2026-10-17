package guard_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/guard"
	"scoped-mcp/mcp-tg/guard/mocks"
)

var allowedChats = []config.Chat{
	{ID: 100, Type: config.ChatTypeUser, AccessHash: 1},
	{ID: 200, Type: config.ChatTypeChat},
	{ID: 300, Type: config.ChatTypeChannel, AccessHash: 3},
}

func TestMiddleware(t *testing.T) {
	allowedUser := &tg.InputPeerUser{UserID: 100, AccessHash: 1}
	allowedChat := &tg.InputPeerChat{ChatID: 200}
	allowedChannel := &tg.InputPeerChannel{ChannelID: 300, AccessHash: 3}
	foreignChannel := &tg.InputPeerChannel{ChannelID: 999, AccessHash: 9}

	tests := []struct {
		name        string
		request     bin.Encoder
		wantAllowed bool
	}{
		{name: "help.getConfig", request: &tg.HelpGetConfigRequest{}, wantAllowed: true},

		{name: "getHistory: разрешённый user", request: &tg.MessagesGetHistoryRequest{Peer: allowedUser}, wantAllowed: true},
		{name: "getHistory: разрешённый chat", request: &tg.MessagesGetHistoryRequest{Peer: allowedChat}, wantAllowed: true},
		{name: "getHistory: разрешённый channel", request: &tg.MessagesGetHistoryRequest{Peer: allowedChannel}, wantAllowed: true},
		{name: "getHistory: чужой channel", request: &tg.MessagesGetHistoryRequest{Peer: foreignChannel}},
		{name: "getHistory: тот же ID, другой тип", request: &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerChannel{ChannelID: 100, AccessHash: 1}}},
		{name: "getHistory: разрешённый ID, другой access_hash", request: &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerChannel{ChannelID: 300, AccessHash: 77}}, wantAllowed: true},
		{name: "getHistory: inputPeerSelf", request: &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerSelf{}}},
		{name: "getHistory: inputPeerEmpty", request: &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerEmpty{}}},
		{name: "getHistory: nil peer", request: &tg.MessagesGetHistoryRequest{}},
		{name: "getHistory: inputPeerUserFromMessage", request: &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerUserFromMessage{Peer: allowedChannel, MsgID: 1, UserID: 100}}},
		{name: "getHistory: inputPeerChannelFromMessage", request: &tg.MessagesGetHistoryRequest{Peer: &tg.InputPeerChannelFromMessage{Peer: allowedChannel, MsgID: 1, ChannelID: 300}}},

		{name: "search: разрешённый channel", request: &tg.MessagesSearchRequest{Peer: allowedChannel, Q: "x"}, wantAllowed: true},
		{name: "search: чужой channel", request: &tg.MessagesSearchRequest{Peer: foreignChannel, Q: "x"}},
		{name: "search: inputPeerEmpty", request: &tg.MessagesSearchRequest{Peer: &tg.InputPeerEmpty{}, Q: "x"}},
		{name: "searchGlobal", request: &tg.MessagesSearchGlobalRequest{Q: "x"}},

		{name: "getReplies: чужой channel", request: &tg.MessagesGetRepliesRequest{Peer: foreignChannel, MsgID: 5}},

		{name: "getForumTopics: разрешённый channel", request: &tg.MessagesGetForumTopicsRequest{Peer: allowedChannel}, wantAllowed: true},
		{name: "getForumTopics: чужой channel", request: &tg.MessagesGetForumTopicsRequest{Peer: foreignChannel}},

		{name: "getPeerDialogs: все разрешены", request: &tg.MessagesGetPeerDialogsRequest{Peers: []tg.InputDialogPeerClass{
			&tg.InputDialogPeer{Peer: allowedUser}, &tg.InputDialogPeer{Peer: allowedChat}, &tg.InputDialogPeer{Peer: allowedChannel},
		}}, wantAllowed: true},
		{name: "getPeerDialogs: разрешённый и чужой", request: &tg.MessagesGetPeerDialogsRequest{Peers: []tg.InputDialogPeerClass{
			&tg.InputDialogPeer{Peer: allowedChannel}, &tg.InputDialogPeer{Peer: foreignChannel},
		}}},
		{name: "getPeerDialogs: folder", request: &tg.MessagesGetPeerDialogsRequest{Peers: []tg.InputDialogPeerClass{&tg.InputDialogPeerFolder{FolderID: 1}}}},
		{name: "getPeerDialogs: community", request: &tg.MessagesGetPeerDialogsRequest{Peers: []tg.InputDialogPeerClass{&tg.InputDialogPeerCommunity{}}}},
		{name: "getPeerDialogs: пустой список", request: &tg.MessagesGetPeerDialogsRequest{}},

		{name: "запись: sendMessage", request: &tg.MessagesSendMessageRequest{Peer: allowedChannel, Message: "hi"}},
		{name: "запись: readHistory", request: &tg.MessagesReadHistoryRequest{Peer: allowedChannel}},
		{name: "запись: readMessageContents", request: &tg.MessagesReadMessageContentsRequest{ID: []int{1}}},
		{name: "запись: channels.readHistory", request: &tg.ChannelsReadHistoryRequest{Channel: &tg.InputChannel{ChannelID: 300, AccessHash: 3}}},
		{name: "служебный: users.getUsers", request: &tg.UsersGetUsersRequest{ID: []tg.InputUserClass{&tg.InputUserSelf{}}}},
		{name: "служебный: messages.getDialogs", request: &tg.MessagesGetDialogsRequest{}},
		{name: "служебный: messages.getDialogFilters", request: &tg.MessagesGetDialogFiltersRequest{}},
		{name: "служебный: contacts.resolveUsername", request: &tg.ContactsResolveUsernameRequest{Username: "x"}},
		{name: "служебный: auth.exportAuthorization", request: &tg.AuthExportAuthorizationRequest{DCID: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextInvoker := mocks.NewMockInvoker(ctrl)
			if tt.wantAllowed {
				nextInvoker.EXPECT().Invoke(gomock.Any(), tt.request, gomock.Any()).Return(nil)
			}

			err := guard.New(allowedChats)(nextInvoker)(t.Context(), tt.request, nil)

			if tt.wantAllowed {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, guard.ErrForbidden)
		})
	}
}

func TestMiddlewareChecksRepliesPeers(t *testing.T) {
	allowedChannelMessage := &tg.Message{ID: 1, PeerID: &tg.PeerChannel{ChannelID: 300}}
	allowedUserMessage := &tg.Message{ID: 2, PeerID: &tg.PeerUser{UserID: 100}}
	allowedChatMessage := &tg.Message{ID: 3, PeerID: &tg.PeerChat{ChatID: 200}}
	foreignChannelMessage := &tg.Message{ID: 4, PeerID: &tg.PeerChannel{ChannelID: 999}}
	sameIDOtherTypeMessage := &tg.Message{ID: 5, PeerID: &tg.PeerChat{ChatID: 300}}
	serviceMessage := &tg.MessageService{ID: 6, PeerID: &tg.PeerChannel{ChannelID: 999}}
	emptyMessage := &tg.MessageEmpty{ID: 7}

	tests := []struct {
		name        string
		replies     tg.MessagesMessagesClass
		wantAllowed bool
	}{
		{name: "сообщения разрешённых peer", replies: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{allowedChannelMessage, allowedUserMessage, allowedChatMessage}}, wantAllowed: true},
		{name: "пустой ответ", replies: &tg.MessagesMessages{}, wantAllowed: true},
		{name: "messageEmpty без peer", replies: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{allowedChannelMessage, emptyMessage}}, wantAllowed: true},
		{name: "notModified", replies: &tg.MessagesMessagesNotModified{Count: 1}, wantAllowed: true},
		{name: "комментарии из чужой группы обсуждения", replies: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{foreignChannelMessage}}},
		{name: "разрешённые и чужое", replies: &tg.MessagesMessagesSlice{Messages: []tg.MessageClass{allowedChannelMessage, foreignChannelMessage}}},
		{name: "тот же ID, другой тип", replies: &tg.MessagesMessages{Messages: []tg.MessageClass{sameIDOtherTypeMessage}}},
		{name: "служебное сообщение чужого peer", replies: &tg.MessagesChannelMessages{Messages: []tg.MessageClass{serviceMessage}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextInvoker := mocks.NewMockInvoker(ctrl)
			request := &tg.MessagesGetRepliesRequest{Peer: &tg.InputPeerChannel{ChannelID: 300, AccessHash: 3}, MsgID: 5}
			nextInvoker.EXPECT().Invoke(gomock.Any(), request, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ bin.Encoder, output bin.Decoder) error {
					output.(*tg.MessagesMessagesBox).Messages = tt.replies
					return nil
				})
			output := &tg.MessagesMessagesBox{}

			err := guard.New(allowedChats)(nextInvoker)(t.Context(), request, output)

			if tt.wantAllowed {
				require.NoError(t, err)
				require.Equal(t, tt.replies, output.Messages)
				return
			}
			require.ErrorIs(t, err, guard.ErrForbidden)
			require.Nil(t, output.Messages)
		})
	}
}

func TestMiddlewarePassesNextError(t *testing.T) {
	ctrl := gomock.NewController(t)
	nextInvoker := mocks.NewMockInvoker(ctrl)
	nextErr := errors.New("downstream")
	request := &tg.HelpGetConfigRequest{}
	nextInvoker.EXPECT().Invoke(gomock.Any(), request, gomock.Any()).Return(nextErr)

	err := guard.New(allowedChats)(nextInvoker)(t.Context(), request, nil)

	require.ErrorIs(t, err, nextErr)
}

func TestMiddlewareRejectsEveryOtherType(t *testing.T) {
	ctrl := gomock.NewController(t)
	nextInvoker := mocks.NewMockInvoker(ctrl)
	nextInvoker.EXPECT().Invoke(gomock.Any(), &tg.HelpGetConfigRequest{}, gomock.Any()).Return(nil).AnyTimes()
	invoke := guard.New(allowedChats)(nextInvoker)

	for typeID, construct := range tg.TypesConstructorMap() {
		input := construct()
		err := invoke(t.Context(), input, nil)
		if typeID == tg.HelpGetConfigRequestTypeID {
			require.NoError(t, err)
			continue
		}
		require.ErrorIs(t, err, guard.ErrForbidden, "type %T must be rejected", input)
	}
}
