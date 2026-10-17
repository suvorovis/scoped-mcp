package tools_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/config"
	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/mcp-tg/telegram/mocks"
	"scoped-mcp/mcp-tg/tools"
)

func TestListTopics(t *testing.T) {
	tests := []struct {
		name        string
		in          tools.ListTopicsInput
		expect      *config.Chat
		reply       []telegram.Topic
		readerErr   error
		want        tools.ListTopicsOutput
		wantErr     error
		wantRevoked bool
	}{
		{name: "топики форума", in: tools.ListTopicsInput{ChatID: 300}, expect: &allowedChannel, reply: []telegram.Topic{{ID: 12, Title: "Incidents"}}, want: tools.ListTopicsOutput{Topics: []telegram.Topic{{ID: 12, Title: "Incidents"}}}},
		{name: "не форум", in: tools.ListTopicsInput{ChatID: 300}, expect: &allowedChannel, readerErr: telegram.ErrNotForum, wantErr: telegram.ErrNotForum},
		{name: "чужой chat_id", in: tools.ListTopicsInput{ChatID: 1}, wantErr: tools.ErrChatNotAllowed},
		{name: "отозванная сессия → ошибка и сигнал", in: tools.ListTopicsInput{ChatID: 300}, expect: &allowedChannel, readerErr: telegram.ErrSessionRevoked, wantErr: telegram.ErrSessionRevoked, wantRevoked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := mocks.NewMockReader(gomock.NewController(t))
			if tt.expect != nil {
				reader.EXPECT().ListTopics(gomock.Any(), *tt.expect).Return(tt.reply, tt.readerErr)
			}
			revoked := false

			got, err := tools.New(reader, allowedChats, func() { revoked = true }).ListTopics(t.Context(), tt.in)

			require.Equal(t, tt.wantRevoked, revoked)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
