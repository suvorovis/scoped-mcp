package telegram_test

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/mcp-tg/telegram/mocks"
)

var (
	userInputPeer  = &tg.InputPeerUser{UserID: 100, AccessHash: 1}
	groupInputPeer = &tg.InputPeerChat{ChatID: 200}

	untitledGroupChat   = config.Chat{ID: 200, Type: config.ChatTypeChat}
	untitledChannelChat = config.Chat{ID: 300, Type: config.ChatTypeChannel, AccessHash: 3}

	sessionRevokedRPCErr = tgerr.New(401, "AUTH_KEY_UNREGISTERED")
)

func folderTitle(text string) tg.TextWithEntities {
	return tg.TextWithEntities{Text: text}
}

func TestListFolders(t *testing.T) {
	tests := []struct {
		name    string
		filters []tg.DialogFilterClass
		rpcErr  error
		want    []telegram.Folder
		wantErr error
	}{
		{
			name: "entities заголовка отбрасываются, признак категорий, chatlist, без «Все чаты»",
			filters: []tg.DialogFilterClass{
				&tg.DialogFilterDefault{},
				&tg.DialogFilter{ID: 3, Title: tg.TextWithEntities{Text: "Claude", Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 6}}}, IncludePeers: []tg.InputPeerClass{userInputPeer}},
				&tg.DialogFilter{ID: 5, Title: folderTitle("Work"), Groups: true, IncludePeers: []tg.InputPeerClass{groupInputPeer}},
				&tg.DialogFilterChatlist{ID: 7, Title: folderTitle("Shared"), IncludePeers: []tg.InputPeerClass{channelInputPeer}},
			},
			want: []telegram.Folder{{ID: 3, Title: "Claude"}, {ID: 5, Title: "Work", HasCategories: true}, {ID: 7, Title: "Shared"}},
		},
		{
			name:    "exclude_muted — тоже категория",
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, Title: folderTitle("Quiet"), ExcludeMuted: true, IncludePeers: []tg.InputPeerClass{userInputPeer}}},
			want:    []telegram.Folder{{ID: 3, Title: "Quiet", HasCategories: true}},
		},
		{
			name:    "папка без явных чатов попадает в список",
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 5, Title: folderTitle("All groups"), Groups: true}},
			want:    []telegram.Folder{{ID: 5, Title: "All groups", HasCategories: true}},
		},
		{name: "без папок → пустой список, не nil", want: []telegram.Folder{}},
		{name: "401 маппится в ErrSessionRevoked", rpcErr: sessionRevokedRPCErr, wantErr: telegram.ErrSessionRevoked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			foldersAPI := mocks.NewMockFoldersAPI(gomock.NewController(t))
			foldersAPI.EXPECT().MessagesGetDialogFilters(gomock.Any()).Return(&tg.MessagesDialogFilters{Filters: tt.filters}, tt.rpcErr)

			got, err := telegram.ListFolders(t.Context(), foldersAPI)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveFolders(t *testing.T) {
	tests := []struct {
		name      string
		wanted    []config.Folder
		filters   []tg.DialogFilterClass
		rpcErr    error
		want      []config.Chat
		wantErr   error
		wantFlood bool
	}{
		{
			name:    "pinned, затем include; access_hash из peer; title папки в чаты не переносится",
			wanted:  []config.Folder{{ID: 3, Title: "Claude"}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, PinnedPeers: []tg.InputPeerClass{channelInputPeer}, IncludePeers: []tg.InputPeerClass{userInputPeer, groupInputPeer}}},
			want:    []config.Chat{untitledChannelChat, userChat, untitledGroupChat},
		},
		{
			name:    "exclude вычитается по (тип, ID)",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{userInputPeer, groupInputPeer}, ExcludePeers: []tg.InputPeerClass{&tg.InputPeerChat{ChatID: 200}}}},
			want:    []config.Chat{userChat},
		},
		{
			name:    "exclude с тем же ID другого типа не вычитает",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{groupInputPeer}, ExcludePeers: []tg.InputPeerClass{&tg.InputPeerChannel{ChannelID: 200, AccessHash: 9}}}},
			want:    []config.Chat{untitledGroupChat},
		},
		{
			name:    "peer и в pinned, и в include возвращается дважды, повторы убирает MergeChats",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, PinnedPeers: []tg.InputPeerClass{channelInputPeer}, IncludePeers: []tg.InputPeerClass{channelInputPeer}}},
			want:    []config.Chat{untitledChannelChat, untitledChannelChat},
		},
		{
			name:    "chatlist-папка",
			wanted:  []config.Folder{{ID: 4}},
			filters: []tg.DialogFilterClass{&tg.DialogFilterChatlist{ID: 4, PinnedPeers: []tg.InputPeerClass{channelInputPeer}, IncludePeers: []tg.InputPeerClass{userInputPeer}}},
			want:    []config.Chat{untitledChannelChat, userChat},
		},
		{
			name:    "inputPeerSelf и inputPeerEmpty пропускаются",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{&tg.InputPeerSelf{}, &tg.InputPeerEmpty{}, userInputPeer}}},
			want:    []config.Chat{userChat},
		},
		{
			name:   "несколько папок в порядке конфига",
			wanted: []config.Folder{{ID: 5}, {ID: 3}},
			filters: []tg.DialogFilterClass{
				&tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{userInputPeer}},
				&tg.DialogFilter{ID: 5, IncludePeers: []tg.InputPeerClass{channelInputPeer}},
			},
			want: []config.Chat{untitledChannelChat, userChat},
		},
		{
			name:    "категории игнорируются, остаются только явные чаты",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, Groups: true, Broadcasts: true, Contacts: true, IncludePeers: []tg.InputPeerClass{userInputPeer}}},
			want:    []config.Chat{userChat},
		},
		{
			name:    "папка только из категорий → ErrFolderNoChats",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, Groups: true}},
			wantErr: telegram.ErrFolderNoChats,
		},
		{
			name:    "все явные чаты в exclude → ErrFolderNoChats",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{userInputPeer}, ExcludePeers: []tg.InputPeerClass{userInputPeer}}},
			wantErr: telegram.ErrFolderNoChats,
		},
		{
			name:    "папка только из Избранного → ErrFolderNoChats",
			wanted:  []config.Folder{{ID: 3}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{&tg.InputPeerSelf{}}}},
			wantErr: telegram.ErrFolderNoChats,
		},
		{
			name:    "неизвестный ID → ErrFolderNotFound",
			wanted:  []config.Folder{{ID: 9}},
			filters: []tg.DialogFilterClass{&tg.DialogFilterDefault{}, &tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{userInputPeer}}},
			wantErr: telegram.ErrFolderNotFound,
		},
		{
			name:    "одна из папок не найдена → ErrFolderNotFound, даже если другая раскрылась",
			wanted:  []config.Folder{{ID: 3}, {ID: 9}},
			filters: []tg.DialogFilterClass{&tg.DialogFilter{ID: 3, IncludePeers: []tg.InputPeerClass{userInputPeer}}},
			wantErr: telegram.ErrFolderNotFound,
		},
		{name: "401 маппится в ErrSessionRevoked", wanted: []config.Folder{{ID: 3}}, rpcErr: sessionRevokedRPCErr, wantErr: telegram.ErrSessionRevoked},
		{name: "FLOOD_WAIT маппится", wanted: []config.Folder{{ID: 3}}, rpcErr: floodWaitRPCErr, wantFlood: true},
		{name: "пустой список папок → без RPC и без чатов"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			foldersAPI := mocks.NewMockFoldersAPI(gomock.NewController(t))
			if len(tt.wanted) > 0 {
				foldersAPI.EXPECT().MessagesGetDialogFilters(gomock.Any()).Return(&tg.MessagesDialogFilters{Filters: tt.filters}, tt.rpcErr)
			}

			got, err := telegram.ResolveFolders(t.Context(), foldersAPI, tt.wanted)

			if tt.wantFlood {
				requireFloodWait(t, err, floodWaitSeconds)
				return
			}
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
