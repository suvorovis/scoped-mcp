package tools_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/confluence"
	"scoped-mcp/mcp-confluence/confluence/mocks"
	"scoped-mcp/mcp-confluence/guard"
	"scoped-mcp/mcp-confluence/tools"
)

var (
	childPage = confluence.Page{ID: "101", Title: "Child", Version: 4, ParentID: "100", AncestorIDs: []string{"1", "100"}, SpaceKey: "DOCS", WebURL: "https://confluence.example.com/display/DOCS/Child", Body: "<p>Текст</p>"}
	rootPage  = confluence.Page{ID: "100", Title: "Root", Version: 2, ParentID: "1", AncestorIDs: []string{"1"}, SpaceKey: "DOCS", WebURL: "https://confluence.example.com/display/DOCS/Root", Body: ""}
)

func TestGetPage(t *testing.T) {
	tests := []struct {
		name    string
		pageID  string
		expect  func(pages *mocks.MockPages)
		want    tools.PageOutput
		wantErr error
	}{
		{
			name: "страница под корнем — с parent_id", pageID: "101",
			expect: func(pages *mocks.MockPages) { pages.EXPECT().GetPage(gomock.Any(), "101").Return(childPage, nil) },
			want:   tools.PageOutput{ID: "101", Title: "Child", Version: 4, ParentID: "100", WebURL: "https://confluence.example.com/display/DOCS/Child", Body: "<p>Текст</p>"},
		},
		{
			name: "корень зоны — без parent_id", pageID: "100",
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().GetPage(gomock.Any(), "100").Return(rootPage, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "1").Return(confluence.ErrNotFound)
			},
			want: tools.PageOutput{ID: "100", Title: "Root", Version: 2, WebURL: "https://confluence.example.com/display/DOCS/Root"},
		},
		{
			name: "корень записи под корнем чтения — с parent_id", pageID: "200",
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().GetPage(gomock.Any(), "200").Return(confluence.Page{ID: "200", Title: "W", Version: 1, ParentID: "101", AncestorIDs: []string{"1", "100", "101"}}, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "101").Return(nil)
			},
			want: tools.PageOutput{ID: "200", Title: "W", Version: 1, ParentID: "101"},
		},
		{
			name: "страница без предков в ответе — без parent_id", pageID: "100",
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().GetPage(gomock.Any(), "100").Return(confluence.Page{ID: "100", Title: "R", Version: 1}, nil)
			},
			want: tools.PageOutput{ID: "100", Title: "R", Version: 1},
		},
		{
			name: "предки в обратном порядке, родитель вне зоны — без parent_id", pageID: "100",
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().GetPage(gomock.Any(), "100").Return(confluence.Page{ID: "100", Title: "R", Version: 1, ParentID: "1", AncestorIDs: []string{"100", "1"}}, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "1").Return(confluence.ErrNotFound)
			},
			want: tools.PageOutput{ID: "100", Title: "R", Version: 1},
		},
		{
			name: "сбой проверки родителя — ошибка", pageID: "200",
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().GetPage(gomock.Any(), "200").Return(confluence.Page{ID: "200", Title: "W", Version: 1, ParentID: "101"}, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "101").Return(confluence.ErrUnavailable)
			},
			wantErr: confluence.ErrUnavailable,
		},
		{
			name: "ошибка клиента пробрасывается", pageID: "101",
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().GetPage(gomock.Any(), "101").Return(confluence.Page{}, confluence.ErrNotFound)
			},
			wantErr: confluence.ErrNotFound,
		},
		{name: "page_id пустой", pageID: "", wantErr: tools.ErrInvalidContentID},
		{name: "page_id не число", pageID: "abc", wantErr: tools.ErrInvalidContentID},
		{name: "page_id с ведущим нулём", pageID: "0101", wantErr: tools.ErrInvalidContentID},
		{name: "page_id отрицательный", pageID: "-1", wantErr: tools.ErrInvalidContentID},
		{name: "page_id из 20 цифр", pageID: "12345678901234567890", wantErr: tools.ErrInvalidContentID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clientMocks := newToolset(t, toolConfig)
			if tt.expect != nil {
				tt.expect(clientMocks.pages)
			}

			got, err := toolset.GetPage(t.Context(), tools.GetPageInput{PageID: tt.pageID})

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestListChildren(t *testing.T) {
	children := confluence.ChildrenBatch{Children: []confluence.PageSummary{{ID: "102", Title: "A"}}, HasMore: true}
	tests := []struct {
		name    string
		input   tools.ListChildrenInput
		expect  func(pages *mocks.MockPages)
		want    tools.ListChildrenOutput
		wantErr error
	}{
		{
			name: "умолчания start 0 и limit 25", input: tools.ListChildrenInput{PageID: "101"},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().ListChildren(gomock.Any(), "101", 0, 25).Return(children, nil)
			},
			want: tools.ListChildrenOutput{Children: []tools.PageSummaryOutput{{ID: "102", Title: "A"}}, HasMore: true},
		},
		{
			name: "свои start и limit", input: tools.ListChildrenInput{PageID: "101", Start: 50, Limit: 100},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().ListChildren(gomock.Any(), "101", 50, 100).Return(confluence.ChildrenBatch{Children: []confluence.PageSummary{}}, nil)
			},
			want: tools.ListChildrenOutput{Children: []tools.PageSummaryOutput{}},
		},
		{name: "limit 101", input: tools.ListChildrenInput{PageID: "101", Limit: 101}, wantErr: tools.ErrInvalidLimit},
		{name: "limit отрицательный", input: tools.ListChildrenInput{PageID: "101", Limit: -1}, wantErr: tools.ErrInvalidLimit},
		{name: "start отрицательный", input: tools.ListChildrenInput{PageID: "101", Start: -1}, wantErr: tools.ErrInvalidStart},
		{name: "page_id не число", input: tools.ListChildrenInput{PageID: "x"}, wantErr: tools.ErrInvalidContentID},
		{
			name: "ошибка клиента", input: tools.ListChildrenInput{PageID: "101"},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().ListChildren(gomock.Any(), "101", 0, 25).Return(confluence.ChildrenBatch{}, confluence.ErrUnavailable)
			},
			wantErr: confluence.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clientMocks := newToolset(t, toolConfig)
			if tt.expect != nil {
				tt.expect(clientMocks.pages)
			}

			got, err := toolset.ListChildren(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

const (
	searchQuery    = `disk "io"`
	searchQueryCQL = `(id = 100 or ancestor = 100 or id = 200 or ancestor = 200) and type = page and text ~ "disk \"io\""`
)

func TestSearch(t *testing.T) {
	found := []confluence.PageSummary{{ID: "102", Title: "A"}, {ID: "300", Title: "Outside"}, {ID: "103", Title: "B"}}
	tests := []struct {
		name    string
		input   tools.SearchInput
		expect  func(pages *mocks.MockPages)
		want    tools.SearchOutput
		wantErr error
	}{
		{
			name: "результат вне зоны отбрасывается молча", input: tools.SearchInput{Query: searchQuery},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().Search(gomock.Any(), searchQueryCQL, 50).Return(found, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "102").Return(nil)
				pages.EXPECT().CheckPage(gomock.Any(), "300").Return(confluence.ErrNotFound)
				pages.EXPECT().CheckPage(gomock.Any(), "103").Return(nil)
			},
			want: tools.SearchOutput{Results: []tools.PageSummaryOutput{{ID: "102", Title: "A"}, {ID: "103", Title: "B"}}},
		},
		{
			name: "результат с id не по шаблону отбрасывается молча", input: tools.SearchInput{Query: searchQuery},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().Search(gomock.Any(), searchQueryCQL, 50).Return([]confluence.PageSummary{{ID: "102", Title: "A"}, {ID: "att501", Title: "Attachment"}, {ID: "", Title: "Empty"}, {ID: "103", Title: "B"}}, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "102").Return(nil)
				pages.EXPECT().CheckPage(gomock.Any(), "103").Return(nil)
			},
			want: tools.SearchOutput{Results: []tools.PageSummaryOutput{{ID: "102", Title: "A"}, {ID: "103", Title: "B"}}},
		},
		{
			name: "свой limit", input: tools.SearchInput{Query: searchQuery, Limit: 5},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().Search(gomock.Any(), searchQueryCQL, 5).Return([]confluence.PageSummary{}, nil)
			},
			want: tools.SearchOutput{Results: []tools.PageSummaryOutput{}},
		},
		{
			name: "сбой перепроверки прерывает поиск", input: tools.SearchInput{Query: searchQuery},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().Search(gomock.Any(), searchQueryCQL, 50).Return(found, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "102").Return(confluence.ErrUnavailable)
			},
			wantErr: confluence.ErrUnavailable,
		},
		{
			name: "отказ токена при перепроверке прерывает поиск", input: tools.SearchInput{Query: searchQuery},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().Search(gomock.Any(), searchQueryCQL, 50).Return(found, nil)
				pages.EXPECT().CheckPage(gomock.Any(), "102").Return(confluence.ErrUnauthorized)
			},
			wantErr: confluence.ErrUnauthorized,
		},
		{
			name: "ошибка поиска", input: tools.SearchInput{Query: searchQuery},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().Search(gomock.Any(), searchQueryCQL, 50).Return(nil, confluence.ErrRejected)
			},
			wantErr: confluence.ErrRejected,
		},
		{name: "пустой запрос", input: tools.SearchInput{Query: ""}, wantErr: tools.ErrInvalidQuery},
		{name: "запрос с переводом строки", input: tools.SearchInput{Query: "a\nb"}, wantErr: tools.ErrInvalidQuery},
		{name: "limit больше max_results", input: tools.SearchInput{Query: "x", Limit: 51}, wantErr: tools.ErrInvalidLimit},
		{name: "limit отрицательный", input: tools.SearchInput{Query: "x", Limit: -1}, wantErr: tools.ErrInvalidLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clientMocks := newToolset(t, toolConfig)
			if tt.expect != nil {
				tt.expect(clientMocks.pages)
			}

			got, err := toolset.Search(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSearchInvalidQueryWrapsGuardError(t *testing.T) {
	toolset, _ := newToolset(t, toolConfig)

	_, err := toolset.Search(t.Context(), tools.SearchInput{Query: ""})

	require.ErrorIs(t, err, tools.ErrInvalidQuery)
	require.ErrorIs(t, err, guard.ErrInvalidSearchQuery)
}

var (
	writtenPage     = confluence.WrittenPage{ID: "202", Title: "Новая", Version: 1, WebURL: "https://confluence.example.com/display/DOCS/Новая"}
	wantWrittenPage = tools.WrittenPageOutput{ID: "202", Title: "Новая", Version: 1, WebURL: "https://confluence.example.com/display/DOCS/Новая"}
	pageDraft       = confluence.PageDraft{Title: "Новая", Body: "<p>x</p>"}
)

func TestCreatePage(t *testing.T) {
	tests := []struct {
		name    string
		input   tools.CreatePageInput
		expect  func(pages *mocks.MockPages)
		want    tools.WrittenPageOutput
		wantErr error
	}{
		{
			name: "ключ пространства берётся у родителя", input: tools.CreatePageInput{ParentID: "201", Title: "Новая", Body: "<p>x</p>"},
			expect: func(pages *mocks.MockPages) {
				gomock.InOrder(
					pages.EXPECT().GetPage(gomock.Any(), "201").Return(confluence.Page{ID: "201", SpaceKey: "DOCS"}, nil),
					pages.EXPECT().CreatePage(gomock.Any(), "201", "DOCS", pageDraft).Return(writtenPage, nil),
				)
			},
			want: wantWrittenPage,
		},
		{
			name: "пустое тело допустимо", input: tools.CreatePageInput{ParentID: "201", Title: "Новая"},
			expect: func(pages *mocks.MockPages) {
				gomock.InOrder(
					pages.EXPECT().GetPage(gomock.Any(), "201").Return(confluence.Page{ID: "201", SpaceKey: "DOCS"}, nil),
					pages.EXPECT().CreatePage(gomock.Any(), "201", "DOCS", confluence.PageDraft{Title: "Новая"}).Return(writtenPage, nil),
				)
			},
			want: wantWrittenPage,
		},
		{
			name: "родитель не найден — создания нет", input: tools.CreatePageInput{ParentID: "300", Title: "Новая"},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().GetPage(gomock.Any(), "300").Return(confluence.Page{}, confluence.ErrNotFound)
			},
			wantErr: confluence.ErrNotFound,
		},
		{
			name: "занятый заголовок", input: tools.CreatePageInput{ParentID: "201", Title: "Новая", Body: "<p>x</p>"},
			expect: func(pages *mocks.MockPages) {
				gomock.InOrder(
					pages.EXPECT().GetPage(gomock.Any(), "201").Return(confluence.Page{ID: "201", SpaceKey: "DOCS"}, nil),
					pages.EXPECT().CreatePage(gomock.Any(), "201", "DOCS", pageDraft).Return(confluence.WrittenPage{}, confluence.ErrTitleTaken),
				)
			},
			wantErr: confluence.ErrTitleTaken,
		},
		{name: "parent_id не число", input: tools.CreatePageInput{ParentID: "abc", Title: "Новая"}, wantErr: tools.ErrInvalidContentID},
		{name: "пустой title", input: tools.CreatePageInput{ParentID: "201", Title: ""}, wantErr: tools.ErrInvalidTitle},
		{name: "title из пробелов", input: tools.CreatePageInput{ParentID: "201", Title: "  "}, wantErr: tools.ErrInvalidTitle},
		{name: "тело больше предела guard", input: tools.CreatePageInput{ParentID: "201", Title: "Новая", Body: strings.Repeat("x", guard.MaxBodyBytes)}, wantErr: tools.ErrInvalidBody},
		{name: "тело больше предела guard после экранирования JSON", input: tools.CreatePageInput{ParentID: "201", Title: "Новая", Body: strings.Repeat("<", guard.MaxBodyBytes/4)}, wantErr: tools.ErrInvalidBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clientMocks := newToolset(t, toolConfig)
			if tt.expect != nil {
				tt.expect(clientMocks.pages)
			}

			got, err := toolset.CreatePage(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestUpdatePage(t *testing.T) {
	tests := []struct {
		name    string
		input   tools.UpdatePageInput
		expect  func(pages *mocks.MockPages)
		want    tools.WrittenPageOutput
		wantErr error
	}{
		{
			name: "прочитанная версия увеличивается на единицу", input: tools.UpdatePageInput{PageID: "201", Version: 3, Title: "Новая", Body: "<p>x</p>"},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().UpdatePage(gomock.Any(), "201", 4, pageDraft).Return(writtenPage, nil)
			},
			want: wantWrittenPage,
		},
		{
			name: "конфликт версий", input: tools.UpdatePageInput{PageID: "201", Version: 3, Title: "Новая", Body: "<p>x</p>"},
			expect: func(pages *mocks.MockPages) {
				pages.EXPECT().UpdatePage(gomock.Any(), "201", 4, pageDraft).Return(confluence.WrittenPage{}, confluence.ErrVersionConflict)
			},
			wantErr: confluence.ErrVersionConflict,
		},
		{name: "version 0", input: tools.UpdatePageInput{PageID: "201", Version: 0, Title: "Новая"}, wantErr: tools.ErrInvalidVersion},
		{name: "version отрицательный", input: tools.UpdatePageInput{PageID: "201", Version: -2, Title: "Новая"}, wantErr: tools.ErrInvalidVersion},
		{name: "page_id не число", input: tools.UpdatePageInput{PageID: "x", Version: 1, Title: "Новая"}, wantErr: tools.ErrInvalidContentID},
		{name: "пустой title", input: tools.UpdatePageInput{PageID: "201", Version: 1, Title: ""}, wantErr: tools.ErrInvalidTitle},
		{name: "version на границе int32", input: tools.UpdatePageInput{PageID: "201", Version: math.MaxInt32, Title: "Новая"}, wantErr: tools.ErrInvalidVersion},
		{name: "тело больше предела guard", input: tools.UpdatePageInput{PageID: "201", Version: 1, Title: "Новая", Body: strings.Repeat("x", guard.MaxBodyBytes)}, wantErr: tools.ErrInvalidBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolset, clientMocks := newToolset(t, toolConfig)
			if tt.expect != nil {
				tt.expect(clientMocks.pages)
			}

			got, err := toolset.UpdatePage(t.Context(), tt.input)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetPageThroughMCP(t *testing.T) {
	toolset, clientMocks := newToolset(t, toolConfig)
	clientMocks.pages.EXPECT().GetPage(gomock.Any(), "101").Return(childPage, nil)

	result, err := connectMCP(t, toolset).CallTool(t.Context(), &mcp.CallToolParams{Name: "get_page", Arguments: map[string]any{"page_id": "101"}})

	require.NoError(t, err)
	require.False(t, result.IsError)
	structured, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"101","title":"Child","version":4,"parent_id":"100","webui":"https://confluence.example.com/display/DOCS/Child","body":"<p>Текст</p>"}`, string(structured))
}

func TestGetPageErrorThroughMCP(t *testing.T) {
	toolset, clientMocks := newToolset(t, toolConfig)
	clientMocks.pages.EXPECT().GetPage(gomock.Any(), "300").Return(confluence.Page{}, confluence.ErrNotFound)

	result, err := connectMCP(t, toolset).CallTool(t.Context(), &mcp.CallToolParams{Name: "get_page", Arguments: map[string]any{"page_id": "300"}})

	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "page not found", text.Text)
}
