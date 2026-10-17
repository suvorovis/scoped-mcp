package main

import (
	"testing"

	"github.com/gotd/td/session"
	"github.com/stretchr/testify/require"

	"scoped-mcp/mcp-tg/config"
)

func TestResolveFolderChatsWithoutFoldersSkipsConnection(t *testing.T) {
	var noClientSession session.Storage

	fromFolders, err := resolveFolderChats(t.Context(), config.Config{}, config.Credentials{}, noClientSession)

	require.NoError(t, err)
	require.Nil(t, fromFolders)
}
