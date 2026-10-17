package tools_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-tg/telegram"
	"scoped-mcp/mcp-tg/telegram/mocks"
	"scoped-mcp/mcp-tg/tools"
)

func msg(id int, date time.Time) telegram.Message {
	return telegram.Message{ID: id, Date: date}
}

func fullPage(n int, newest time.Time) []telegram.Message {
	out := make([]telegram.Message, 0, n)
	for i := range n {
		out = append(out, msg(n-i, newest.Add(-time.Duration(i)*time.Minute)))
	}
	return out
}

func pageOf(messages []telegram.Message) telegram.Page {
	page := telegram.Page{Messages: messages, RawCount: len(messages)}
	for _, message := range messages {
		if page.OldestRawID == 0 || message.ID < page.OldestRawID {
			page.OldestRawID = message.ID
		}
	}
	return page
}

func TestGetMessages(t *testing.T) {
	t10 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	t09 := t10.Add(-time.Hour)
	t08 := t10.Add(-2 * time.Hour)
	floodErr := &telegram.FloodWaitError{Wait: 3 * time.Second}
	tests := []struct {
		name        string
		in          tools.GetMessagesInput
		expect      *telegram.MessagesRequest
		reply       telegram.Page
		readerErr   error
		want        tools.MessagesOutput
		wantErr     error
		wantRevoked bool
	}{
		{
			name:   "лимит по умолчанию 50, полная страница даёт next_before_id",
			in:     tools.GetMessagesInput{ChatID: 300},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 50},
			reply:  pageOf(fullPage(50, t10)),
			want:   tools.MessagesOutput{Messages: fullPage(50, t10), NextBeforeID: 1},
		},
		{
			name:   "лимит выше максимума режется до 100",
			in:     tools.GetMessagesInput{ChatID: 300, Limit: 500},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 100},
			reply:  pageOf(fullPage(100, t10)),
			want:   tools.MessagesOutput{Messages: fullPage(100, t10), NextBeforeID: 1},
		},
		{
			name:   "отрицательный лимит → по умолчанию",
			in:     tools.GetMessagesInput{ChatID: 300, Limit: -1},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 50},
			reply:  pageOf([]telegram.Message{}),
			want:   tools.MessagesOutput{Messages: []telegram.Message{}},
		},
		{
			name:   "короткая сырая страница без next_before_id",
			in:     tools.GetMessagesInput{ChatID: 300, Limit: 3},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 3},
			reply:  telegram.Page{Messages: []telegram.Message{msg(30, t10)}, RawCount: 2, OldestRawID: 20},
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(30, t10)}},
		},
		{
			name:   "полная сырая страница со служебными сообщениями даёт next_before_id = OldestRawID",
			in:     tools.GetMessagesInput{ChatID: 300, Limit: 3},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 3},
			reply:  telegram.Page{Messages: []telegram.Message{msg(30, t10), msg(20, t09)}, RawCount: 3, OldestRawID: 10},
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(30, t10), msg(20, t09)}, NextBeforeID: 10},
		},
		{
			name:   "before_id и topic_id передаются читателю",
			in:     tools.GetMessagesInput{ChatID: 300, TopicID: 12, BeforeID: 4512, Limit: 2},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, TopicID: 12, OffsetID: 4512, Limit: 2},
			reply:  pageOf([]telegram.Message{msg(4511, t10), msg(4510, t09)}),
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(4511, t10), msg(4510, t09)}, NextBeforeID: 4510},
		},
		{
			name:   "since отрезает старые и убирает next_before_id",
			in:     tools.GetMessagesInput{ChatID: 300, Limit: 3, Since: t09.Format(time.RFC3339)},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 3},
			reply:  pageOf([]telegram.Message{msg(30, t10), msg(20, t09), msg(10, t08)}),
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(30, t10), msg(20, t09)}},
		},
		{
			name:   "сообщение с date == since сохраняется, страница полная",
			in:     tools.GetMessagesInput{ChatID: 300, Limit: 2, Since: t09.Format(time.RFC3339)},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 2},
			reply:  pageOf([]telegram.Message{msg(30, t10), msg(20, t09)}),
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(30, t10), msg(20, t09)}, NextBeforeID: 20},
		},
		{
			name:   "since раньше всех сообщений — страница полная, next_before_id есть",
			in:     tools.GetMessagesInput{ChatID: 300, Limit: 2, Since: t08.Add(-time.Minute).Format(time.RFC3339)},
			expect: &telegram.MessagesRequest{Chat: allowedChannel, Limit: 2},
			reply:  pageOf([]telegram.Message{msg(30, t10), msg(20, t09)}),
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(30, t10), msg(20, t09)}, NextBeforeID: 20},
		},
		{
			name:    "since не RFC 3339",
			in:      tools.GetMessagesInput{ChatID: 300, Since: "вчера"},
			wantErr: tools.ErrInvalidSince,
		},
		{
			name:    "чужой chat_id → ErrChatNotAllowed без обращения к читателю",
			in:      tools.GetMessagesInput{ChatID: 999},
			wantErr: tools.ErrChatNotAllowed,
		},
		{
			name:      "flood wait читателя пробрасывается",
			in:        tools.GetMessagesInput{ChatID: 300},
			expect:    &telegram.MessagesRequest{Chat: allowedChannel, Limit: 50},
			readerErr: floodErr,
			wantErr:   floodErr,
		},
		{
			name:        "отозванная сессия вызывает onSessionRevoked",
			in:          tools.GetMessagesInput{ChatID: 300},
			expect:      &telegram.MessagesRequest{Chat: allowedChannel, Limit: 50},
			readerErr:   telegram.ErrSessionRevoked,
			wantErr:     telegram.ErrSessionRevoked,
			wantRevoked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := mocks.NewMockReader(gomock.NewController(t))
			if tt.expect != nil {
				reader.EXPECT().GetMessages(gomock.Any(), *tt.expect).Return(tt.reply, tt.readerErr)
			}
			revoked := false

			got, err := tools.New(reader, allowedChats, func() { revoked = true }).GetMessages(t.Context(), tt.in)

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

func TestSearchMessages(t *testing.T) {
	t10 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		in          tools.SearchMessagesInput
		expect      *telegram.SearchRequest
		reply       telegram.Page
		readerErr   error
		want        tools.MessagesOutput
		wantErr     error
		wantRevoked bool
	}{
		{
			name:   "запрос, топик и before_id передаются; полная страница",
			in:     tools.SearchMessagesInput{ChatID: 300, Query: "откат", TopicID: 12, BeforeID: 4512, Limit: 2},
			expect: &telegram.SearchRequest{Chat: allowedChannel, Query: "откат", TopicID: 12, OffsetID: 4512, Limit: 2},
			reply:  pageOf([]telegram.Message{msg(4511, t10), msg(4500, t10)}),
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(4511, t10), msg(4500, t10)}, NextBeforeID: 4500},
		},
		{
			name:   "лимит по умолчанию, короткая страница",
			in:     tools.SearchMessagesInput{ChatID: 300, Query: "x"},
			expect: &telegram.SearchRequest{Chat: allowedChannel, Query: "x", Limit: 50},
			reply:  pageOf([]telegram.Message{msg(1, t10)}),
			want:   tools.MessagesOutput{Messages: []telegram.Message{msg(1, t10)}},
		},
		{name: "чужой chat_id", in: tools.SearchMessagesInput{ChatID: 1, Query: "x"}, wantErr: tools.ErrChatNotAllowed},
		{
			name:        "отозванная сессия вызывает onSessionRevoked",
			in:          tools.SearchMessagesInput{ChatID: 300, Query: "x"},
			expect:      &telegram.SearchRequest{Chat: allowedChannel, Query: "x", Limit: 50},
			readerErr:   telegram.ErrSessionRevoked,
			wantErr:     telegram.ErrSessionRevoked,
			wantRevoked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := mocks.NewMockReader(gomock.NewController(t))
			if tt.expect != nil {
				reader.EXPECT().SearchMessages(gomock.Any(), *tt.expect).Return(tt.reply, tt.readerErr)
			}
			revoked := false

			got, err := tools.New(reader, allowedChats, func() { revoked = true }).SearchMessages(t.Context(), tt.in)

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
