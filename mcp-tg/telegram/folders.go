package telegram

import (
	"context"
	"fmt"
	"slices"

	"github.com/gotd/td/tg"

	"scoped-mcp/mcp-tg/config"
)

type FoldersAPI interface {
	MessagesGetDialogFilters(ctx context.Context) (*tg.MessagesDialogFilters, error)
}

var _ FoldersAPI = (*tg.Client)(nil)

type Folder struct {
	ID            int
	Title         string
	HasCategories bool
}

type folderPeers struct {
	pinned  []tg.InputPeerClass
	include []tg.InputPeerClass
	exclude []tg.InputPeerClass
}

func ListFolders(ctx context.Context, foldersAPI FoldersAPI) ([]Folder, error) {
	result, err := foldersAPI.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	folders := make([]Folder, 0, len(result.Filters))
	for _, filter := range result.Filters {
		switch dialogFilter := filter.(type) {
		case *tg.DialogFilter:
			folders = append(folders, Folder{ID: dialogFilter.ID, Title: dialogFilter.Title.Text, HasCategories: hasCategories(dialogFilter)})
		case *tg.DialogFilterChatlist:
			folders = append(folders, Folder{ID: dialogFilter.ID, Title: dialogFilter.Title.Text})
		}
	}
	return folders, nil
}

func hasCategories(filter *tg.DialogFilter) bool {
	return filter.Contacts || filter.NonContacts || filter.Groups || filter.Broadcasts || filter.Bots ||
		filter.ExcludeMuted || filter.ExcludeRead || filter.ExcludeArchived
}

func ResolveFolders(ctx context.Context, foldersAPI FoldersAPI, wanted []config.Folder) ([]config.Chat, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	result, err := foldersAPI.MessagesGetDialogFilters(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	peersByFolderID := explicitPeersByFolderID(result.Filters)
	var chats []config.Chat
	for _, folder := range wanted {
		peers, found := peersByFolderID[folder.ID]
		if !found {
			return nil, fmt.Errorf("%w: %d", ErrFolderNotFound, folder.ID)
		}
		folderChats := peers.chats()
		if len(folderChats) == 0 {
			return nil, fmt.Errorf("%w: %d", ErrFolderNoChats, folder.ID)
		}
		chats = append(chats, folderChats...)
	}
	return chats, nil
}

func explicitPeersByFolderID(filters []tg.DialogFilterClass) map[int]folderPeers {
	byID := make(map[int]folderPeers, len(filters))
	for _, filter := range filters {
		switch dialogFilter := filter.(type) {
		case *tg.DialogFilter:
			byID[dialogFilter.ID] = folderPeers{pinned: dialogFilter.PinnedPeers, include: dialogFilter.IncludePeers, exclude: dialogFilter.ExcludePeers}
		case *tg.DialogFilterChatlist:
			byID[dialogFilter.ID] = folderPeers{pinned: dialogFilter.PinnedPeers, include: dialogFilter.IncludePeers}
		}
	}
	return byID
}

func (p folderPeers) chats() []config.Chat {
	excluded := make(map[config.ChatKey]struct{}, len(p.exclude))
	for _, chat := range chatsFromInputPeers(p.exclude) {
		excluded[chat.Key()] = struct{}{}
	}
	var chats []config.Chat
	for _, chat := range chatsFromInputPeers(slices.Concat(p.pinned, p.include)) {
		if _, isExcluded := excluded[chat.Key()]; isExcluded {
			continue
		}
		chats = append(chats, chat)
	}
	return chats
}

func chatsFromInputPeers(peers []tg.InputPeerClass) []config.Chat {
	chats := make([]config.Chat, 0, len(peers))
	for _, peer := range peers {
		if chat, ok := ChatFromInputPeer(peer); ok {
			chats = append(chats, chat)
		}
	}
	return chats
}
