package telegram_test

import (
	"context"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/mcp-tg/telegram/mocks"
)

const floodWaitSeconds = 7

var (
	channelChat      = config.Chat{ID: 300, Type: config.ChatTypeChannel, AccessHash: 3, Title: "from config"}
	channelInputPeer = &tg.InputPeerChannel{ChannelID: 300, AccessHash: 3}
	groupChat        = config.Chat{ID: 200, Type: config.ChatTypeChat, Title: "group from config"}
	userChat         = config.Chat{ID: 100, Type: config.ChatTypeUser, AccessHash: 1}

	floodWaitRPCErr = tgerr.New(420, "FLOOD_WAIT_7")
)

func requireFloodWait(t *testing.T, err error, wantSeconds int) {
	t.Helper()
	var floodErr *telegram.FloodWaitError
	require.ErrorAs(t, err, &floodErr)
	require.Equal(t, time.Duration(wantSeconds)*time.Second, floodErr.Wait)
}

func TestReaderMessages(t *testing.T) {
	rpcResult := &tg.MessagesChannelMessages{Messages: []tg.MessageClass{&tg.Message{ID: 7, Date: 1, PeerID: &tg.PeerChannel{ChannelID: 300}}}}
	tests := []struct {
		name      string
		expect    func(api *mocks.MockAPI)
		call      func(ctx context.Context, reader telegram.Reader) (telegram.Page, error)
		wantIDs   []int
		wantErr   error
		wantFlood bool
	}{
		{
			name: "история без топика → messages.getHistory",
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesGetHistory(gomock.Any(), &tg.MessagesGetHistoryRequest{Peer: channelInputPeer, OffsetID: 500, Limit: 50}).Return(rpcResult, nil)
			},
			call: func(ctx context.Context, reader telegram.Reader) (telegram.Page, error) {
				return reader.GetMessages(ctx, telegram.MessagesRequest{Chat: channelChat, OffsetID: 500, Limit: 50})
			},
			wantIDs: []int{7},
		},
		{
			name: "история топика → messages.getReplies",
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesGetReplies(gomock.Any(), &tg.MessagesGetRepliesRequest{Peer: channelInputPeer, MsgID: 12, OffsetID: 500, Limit: 50}).Return(rpcResult, nil)
			},
			call: func(ctx context.Context, reader telegram.Reader) (telegram.Page, error) {
				return reader.GetMessages(ctx, telegram.MessagesRequest{Chat: channelChat, TopicID: 12, OffsetID: 500, Limit: 50})
			},
			wantIDs: []int{7},
		},
		{
			name: "поиск → messages.search с top_msg_id и пустым фильтром",
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesSearch(gomock.Any(), &tg.MessagesSearchRequest{
					Peer: channelInputPeer, Q: "откат", Filter: &tg.InputMessagesFilterEmpty{}, TopMsgID: 12, OffsetID: 500, Limit: 20,
				}).Return(rpcResult, nil)
			},
			call: func(ctx context.Context, reader telegram.Reader) (telegram.Page, error) {
				return reader.SearchMessages(ctx, telegram.SearchRequest{Chat: channelChat, Query: "откат", TopicID: 12, OffsetID: 500, Limit: 20})
			},
			wantIDs: []int{7},
		},
		{
			name: "FLOOD_WAIT маппится",
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesGetHistory(gomock.Any(), gomock.Any()).Return(nil, floodWaitRPCErr)
			},
			call: func(ctx context.Context, reader telegram.Reader) (telegram.Page, error) {
				return reader.GetMessages(ctx, telegram.MessagesRequest{Chat: channelChat, Limit: 50})
			},
			wantFlood: true,
		},
		{
			name: "401 маппится в ErrSessionRevoked",
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesSearch(gomock.Any(), gomock.Any()).Return(nil, tgerr.New(401, "AUTH_KEY_UNREGISTERED"))
			},
			call: func(ctx context.Context, reader telegram.Reader) (telegram.Page, error) {
				return reader.SearchMessages(ctx, telegram.SearchRequest{Chat: channelChat, Query: "x", Limit: 50})
			},
			wantErr: telegram.ErrSessionRevoked,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := mocks.NewMockAPI(gomock.NewController(t))
			tt.expect(api)

			got, err := tt.call(t.Context(), telegram.NewReader(api))

			if tt.wantFlood {
				requireFloodWait(t, err, floodWaitSeconds)
				return
			}
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			ids := make([]int, 0, len(got.Messages))
			for _, m := range got.Messages {
				ids = append(ids, m.ID)
			}
			require.Equal(t, tt.wantIDs, ids)
		})
	}
}

func TestReaderListTopics(t *testing.T) {
	tests := []struct {
		name    string
		chat    config.Chat
		expect  func(api *mocks.MockAPI)
		want    []telegram.Topic
		wantErr error
	}{
		{
			name: "форум канала",
			chat: channelChat,
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesGetForumTopics(gomock.Any(), &tg.MessagesGetForumTopicsRequest{Peer: channelInputPeer, Limit: 100}).
					Return(&tg.MessagesForumTopics{Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 12, Title: "Incidents"}}}, nil)
			},
			want: []telegram.Topic{{ID: 12, Title: "Incidents"}},
		},
		{
			name: "канал без форума → ErrNotForum",
			chat: channelChat,
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesGetForumTopics(gomock.Any(), gomock.Any()).Return(nil, tgerr.New(400, "CHANNEL_FORUM_MISSING"))
			},
			wantErr: telegram.ErrNotForum,
		},
		{name: "базовая группа → ErrNotForum без RPC", chat: groupChat, expect: func(*mocks.MockAPI) {}, wantErr: telegram.ErrNotForum},
		{name: "пользователь → ErrNotForum без RPC", chat: userChat, expect: func(*mocks.MockAPI) {}, wantErr: telegram.ErrNotForum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := mocks.NewMockAPI(gomock.NewController(t))
			tt.expect(api)

			got, err := telegram.NewReader(api).ListTopics(t.Context(), tt.chat)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestReaderListChats(t *testing.T) {
	chats := []config.Chat{channelChat, groupChat, userChat}
	peers := []tg.InputDialogPeerClass{
		&tg.InputDialogPeer{Peer: channelInputPeer},
		&tg.InputDialogPeer{Peer: &tg.InputPeerChat{ChatID: 200}},
		&tg.InputDialogPeer{Peer: &tg.InputPeerUser{UserID: 100, AccessHash: 1}},
	}
	tests := []struct {
		name      string
		expect    func(api *mocks.MockAPI)
		want      []telegram.Chat
		wantFlood bool
	}{
		{
			name: "названия из ответа, иначе из конфига",
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesGetPeerDialogs(gomock.Any(), peers).Return(&tg.MessagesPeerDialogs{
					Chats: []tg.ChatClass{&tg.Channel{ID: 300, Title: "Team oncall"}},
					Users: []tg.UserClass{&tg.User{ID: 100, FirstName: "Ivan"}},
				}, nil)
			},
			want: []telegram.Chat{
				{ID: 300, Title: "Team oncall", Type: config.ChatTypeChannel},
				{ID: 200, Title: "group from config", Type: config.ChatTypeChat},
				{ID: 100, Title: "Ivan", Type: config.ChatTypeUser},
			},
		},
		{
			name: "FLOOD_WAIT от RPC маппится",
			expect: func(api *mocks.MockAPI) {
				api.EXPECT().MessagesGetPeerDialogs(gomock.Any(), peers).Return(nil, floodWaitRPCErr)
			},
			wantFlood: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := mocks.NewMockAPI(gomock.NewController(t))
			tt.expect(api)

			got, err := telegram.NewReader(api).ListChats(t.Context(), chats)

			if tt.wantFlood {
				requireFloodWait(t, err, floodWaitSeconds)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
