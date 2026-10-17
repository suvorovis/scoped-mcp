package main

import (
	"context"
	"fmt"
	"io"

	"github.com/gotd/td/session"
	"gopkg.in/yaml.v3"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/guard"
	"scoped-mcp/mcp-tg/telegram"
)

const categoriesIgnoredComment = "categories are ignored, only chats added to the folder explicitly are read"

func runFolders(ctx context.Context, creds config.Credentials, clientSession session.Storage, stdout io.Writer) error {
	if err := requireSession(ctx, clientSession); err != nil {
		return err
	}
	tgClient := newTelegramClient(creds, clientSession, guard.NewFolderReader())
	return tgClient.Run(ctx, func(ctx context.Context) error {
		folders, err := telegram.ListFolders(ctx, tgClient.API())
		if err != nil {
			return fmt.Errorf("list folders: %w", err)
		}
		return writeFolders(stdout, folders)
	})
}

func writeFolders(w io.Writer, folders []telegram.Folder) error {
	document, err := foldersDocumentNode(folders)
	if err != nil {
		return err
	}
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(yamlIndent)
	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("encode folders: %w", err)
	}
	return encoder.Close()
}

func foldersDocumentNode(folders []telegram.Folder) (*yaml.Node, error) {
	entries := &yaml.Node{Kind: yaml.SequenceNode}
	for _, folder := range folders {
		entry, err := folderEntry(folder)
		if err != nil {
			return nil, err
		}
		entries.Content = append(entries.Content, entry)
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "folders"}, entries}}
	return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}, nil
}

func folderEntry(folder telegram.Folder) (*yaml.Node, error) {
	entry := &yaml.Node{}
	if err := entry.Encode(config.Folder{ID: folder.ID, Title: folder.Title}); err != nil {
		return nil, fmt.Errorf("encode folder %d: %w", folder.ID, err)
	}
	if folder.HasCategories {
		lastField := entry.Content[len(entry.Content)-1]
		lastField.FootComment = categoriesIgnoredComment
	}
	return entry, nil
}
