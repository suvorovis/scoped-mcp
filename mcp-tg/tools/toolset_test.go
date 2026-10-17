package tools_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/mcp-tg/telegram/mocks"
	"scoped-mcp/mcp-tg/tools"
)

var (
	allowedChannel = config.Chat{ID: 300, Type: config.ChatTypeChannel, AccessHash: 3, Title: "Team oncall"}
	allowedChats   = []config.Chat{allowedChannel}
)

func connectMCP(t *testing.T, toolset *tools.Toolset) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "mcp-tg-test", Version: "test"}, nil)
	toolset.Register(server)
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession
}

func TestRegisterListsReadOnlyTools(t *testing.T) {
	reader := mocks.NewMockReader(gomock.NewController(t))
	session := connectMCP(t, tools.New(reader, allowedChats, nil))

	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)

	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		require.NotNil(t, tool.Annotations, tool.Name)
		require.True(t, tool.Annotations.ReadOnlyHint, tool.Name)
	}
	require.ElementsMatch(t, []string{"list_chats", "list_topics", "get_messages", "search_messages"}, names)
}

func TestToolCallsOverMCP(t *testing.T) {
	date := time.Date(2026, 10, 14, 9, 12, 3, 0, time.UTC)
	tests := []struct {
		name      string
		tool      string
		args      map[string]any
		expect    func(reader *mocks.MockReader)
		wantJSON  string
		wantError bool
	}{
		{
			name: "list_chats",
			tool: "list_chats",
			expect: func(reader *mocks.MockReader) {
				reader.EXPECT().ListChats(gomock.Any(), allowedChats).Return([]telegram.Chat{{ID: 300, Title: "Team oncall", Type: "channel"}}, nil)
			},
			wantJSON: `{"chats":[{"id":300,"title":"Team oncall","type":"channel"}]}`,
		},
		{
			name: "list_topics",
			tool: "list_topics",
			args: map[string]any{"chat_id": 300},
			expect: func(reader *mocks.MockReader) {
				reader.EXPECT().ListTopics(gomock.Any(), allowedChannel).Return([]telegram.Topic{{ID: 12, Title: "Incidents", Closed: true}}, nil)
			},
			wantJSON: `{"topics":[{"id":12,"title":"Incidents","closed":true}]}`,
		},
		{
			name: "get_messages: все поля сообщения, next_before_id присутствует",
			tool: "get_messages",
			args: map[string]any{"chat_id": 300, "limit": 1},
			expect: func(reader *mocks.MockReader) {
				reader.EXPECT().GetMessages(gomock.Any(), telegram.MessagesRequest{Chat: allowedChannel, Limit: 1}).Return(telegram.Page{
					Messages: []telegram.Message{
						{ID: 4512, Date: date, From: &telegram.Author{ID: 777, Name: "Ivan Petrov"}, Text: "релиз откатили", ReplyToID: 4509, TopicID: 12, ForwardedFrom: "Team alerts", Media: "document: report.pdf", Outgoing: true},
					},
					RawCount:    1,
					OldestRawID: 4512,
				}, nil)
			},
			wantJSON: `{"messages":[{"id":4512,"date":"2026-10-14T09:12:03Z","from":{"id":777,"name":"Ivan Petrov"},"text":"релиз откатили","reply_to_id":4509,"topic_id":12,"forwarded_from":"Team alerts","media":"document: report.pdf","outgoing":true}],"next_before_id":4512}`,
		},
		{
			name: "get_messages: короткая страница без next_before_id, пустые поля опущены",
			tool: "get_messages",
			args: map[string]any{"chat_id": 300, "limit": 2},
			expect: func(reader *mocks.MockReader) {
				reader.EXPECT().GetMessages(gomock.Any(), telegram.MessagesRequest{Chat: allowedChannel, Limit: 2}).Return(telegram.Page{
					Messages:    []telegram.Message{{ID: 1, Date: date, Text: "x"}},
					RawCount:    1,
					OldestRawID: 1,
				}, nil)
			},
			wantJSON: `{"messages":[{"id":1,"date":"2026-10-14T09:12:03Z","text":"x"}]}`,
		},
		{
			name: "search_messages",
			tool: "search_messages",
			args: map[string]any{"chat_id": 300, "query": "откат"},
			expect: func(reader *mocks.MockReader) {
				reader.EXPECT().SearchMessages(gomock.Any(), telegram.SearchRequest{Chat: allowedChannel, Query: "откат", Limit: 50}).Return(telegram.Page{Messages: []telegram.Message{}}, nil)
			},
			wantJSON: `{"messages":[]}`,
		},
		{name: "get_messages без chat_id — IsError", tool: "get_messages", args: map[string]any{}, wantError: true},
		{name: "get_messages чужого чата — IsError", tool: "get_messages", args: map[string]any{"chat_id": 1}, wantError: true},
		{
			name: "ошибка читателя — IsError",
			tool: "list_topics",
			args: map[string]any{"chat_id": 300},
			expect: func(reader *mocks.MockReader) {
				reader.EXPECT().ListTopics(gomock.Any(), allowedChannel).Return(nil, telegram.ErrNotForum)
			},
			wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := mocks.NewMockReader(gomock.NewController(t))
			if tt.expect != nil {
				tt.expect(reader)
			}
			session := connectMCP(t, tools.New(reader, allowedChats, nil))

			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})

			require.NoError(t, err)
			require.Equal(t, tt.wantError, result.IsError)
			if tt.wantError {
				require.NotEmpty(t, result.Content)
				return
			}
			structured, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantJSON, string(structured))
		})
	}
}
