package guard_test

import (
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/guard"
	"scoped-mcp/mcp-tg/guard/mocks"
)

func TestFolderReader(t *testing.T) {
	channel := &tg.InputPeerChannel{ChannelID: 300, AccessHash: 3}
	tests := []struct {
		name        string
		request     bin.Encoder
		wantAllowed bool
	}{
		{name: "help.getConfig", request: &tg.HelpGetConfigRequest{}, wantAllowed: true},
		{name: "messages.getDialogFilters", request: &tg.MessagesGetDialogFiltersRequest{}, wantAllowed: true},

		{name: "чтение: getHistory", request: &tg.MessagesGetHistoryRequest{Peer: channel}},
		{name: "чтение: search", request: &tg.MessagesSearchRequest{Peer: channel, Q: "x"}},
		{name: "чтение: getReplies", request: &tg.MessagesGetRepliesRequest{Peer: channel, MsgID: 5}},
		{name: "чтение: getForumTopics", request: &tg.MessagesGetForumTopicsRequest{Peer: channel}},
		{name: "чтение: getPeerDialogs", request: &tg.MessagesGetPeerDialogsRequest{Peers: []tg.InputDialogPeerClass{&tg.InputDialogPeer{Peer: channel}}}},
		{name: "служебный: messages.getDialogs", request: &tg.MessagesGetDialogsRequest{}},
		{name: "служебный: users.getUsers", request: &tg.UsersGetUsersRequest{ID: []tg.InputUserClass{&tg.InputUserSelf{}}}},
		{name: "запись: messages.updateDialogFilter", request: &tg.MessagesUpdateDialogFilterRequest{ID: 3}},
		{name: "запись: messages.updateDialogFiltersOrder", request: &tg.MessagesUpdateDialogFiltersOrderRequest{Order: []int{3}}},
		{name: "запись: sendMessage", request: &tg.MessagesSendMessageRequest{Peer: channel, Message: "hi"}},
		{name: "запись: readHistory", request: &tg.MessagesReadHistoryRequest{Peer: channel}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			nextInvoker := mocks.NewMockInvoker(ctrl)
			if tt.wantAllowed {
				nextInvoker.EXPECT().Invoke(gomock.Any(), tt.request, gomock.Any()).Return(nil)
			}

			err := guard.NewFolderReader()(nextInvoker)(t.Context(), tt.request, nil)

			if tt.wantAllowed {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, guard.ErrForbidden)
		})
	}
}

func TestFolderReaderPassesNextError(t *testing.T) {
	ctrl := gomock.NewController(t)
	nextInvoker := mocks.NewMockInvoker(ctrl)
	nextErr := errors.New("downstream")
	request := &tg.MessagesGetDialogFiltersRequest{}
	nextInvoker.EXPECT().Invoke(gomock.Any(), request, gomock.Any()).Return(nextErr)

	err := guard.NewFolderReader()(nextInvoker)(t.Context(), request, nil)

	require.ErrorIs(t, err, nextErr)
}

func TestFolderReaderRejectsEveryOtherType(t *testing.T) {
	ctrl := gomock.NewController(t)
	nextInvoker := mocks.NewMockInvoker(ctrl)
	allowedTypeIDs := map[uint32]struct{}{
		tg.HelpGetConfigRequestTypeID:            {},
		tg.MessagesGetDialogFiltersRequestTypeID: {},
	}
	isAllowedRequest := gomock.Cond(func(request interface{ TypeID() uint32 }) bool {
		_, allowed := allowedTypeIDs[request.TypeID()]
		return allowed
	})
	nextInvoker.EXPECT().Invoke(gomock.Any(), isAllowedRequest, gomock.Any()).Return(nil).AnyTimes()
	invoke := guard.NewFolderReader()(nextInvoker)

	for typeID, construct := range tg.TypesConstructorMap() {
		input := construct()
		err := invoke(t.Context(), input, nil)
		if _, allowed := allowedTypeIDs[typeID]; allowed {
			require.NoError(t, err)
			continue
		}
		require.ErrorIs(t, err, guard.ErrForbidden, "type %T must be rejected", input)
	}
}
