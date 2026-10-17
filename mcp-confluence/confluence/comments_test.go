package confluence_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"scoped-mcp/mcp-confluence/confluence"
)

func commentsURL(start int) string {
	return baseURL + "/rest/api/content/101/child/comment?depth=all&expand=body.storage%2Chistory%2Cancestors&limit=100&start=" + strconv.Itoa(start)
}

const (
	rootCommentJSON = `{"id":"901","type":"comment","status":"current","title":"Re: Child","history":{"createdBy":{"username":"u1","displayName":"User One"},"createdDate":"2026-10-03T12:00:00.000+03:00"},` +
		`"body":{"storage":{"value":"<p>root</p>","representation":"storage"}},"ancestors":[{"id":"101","type":"page","title":"Child"}],"extensions":{"location":"footer"}}`
	replyCommentJSON = `{"id":"902","type":"comment","status":"current","history":{"createdBy":{"displayName":"User Two"},"createdDate":"2026-10-03T13:00:00.000+03:00"},` +
		`"body":{"storage":{"value":"<p>reply</p>"}},"ancestors":[{"id":"101","type":"page"},{"id":"901","type":"comment"}]}`
	deepReplyJSON = `{"id":"903","type":"comment","history":{"createdBy":{"displayName":"User One"},"createdDate":"2026-10-03T14:00:00.000+03:00"},` +
		`"body":{"storage":{"value":"<p>deep</p>"}},"ancestors":[{"id":"101","type":"page"},{"id":"901","type":"comment"},{"id":"902","type":"comment"}]}`
)

var (
	rootComment  = confluence.Comment{ID: "901", Author: "User One", Created: "2026-10-03T12:00:00.000+03:00", Body: "<p>root</p>"}
	replyComment = confluence.Comment{ID: "902", ParentCommentID: "901", Author: "User Two", Created: "2026-10-03T13:00:00.000+03:00", Body: "<p>reply</p>"}
	deepReply    = confluence.Comment{ID: "903", ParentCommentID: "902", Author: "User One", Created: "2026-10-03T14:00:00.000+03:00", Body: "<p>deep</p>"}
)

func TestListComments(t *testing.T) {
	tests := []struct {
		name    string
		batches []listedBatch
		want    []confluence.Comment
		wantErr error
	}{
		{
			name:    "одна пачка: корневой, ответ, ответ на ответ",
			batches: singleBatch(jsonResponse(http.StatusOK, `{"results":[`+rootCommentJSON+`,`+replyCommentJSON+`,`+deepReplyJSON+`],"_links":{}}`)),
			want:    []confluence.Comment{rootComment, replyComment, deepReply},
		},
		{
			name: "две пачки по next: start второй — число полученных, а не limit",
			batches: []listedBatch{
				{start: 0, response: jsonResponse(http.StatusOK, `{"results":[`+rootCommentJSON+`],"_links":{"next":"/rest/api/content/101/child/comment?start=1"}}`)},
				{start: 1, response: jsonResponse(http.StatusOK, `{"results":[`+replyCommentJSON+`],"_links":{}}`)},
			},
			want: []confluence.Comment{rootComment, replyComment},
		},
		{name: "без комментариев", batches: singleBatch(jsonResponse(http.StatusOK, `{"results":[]}`)), want: []confluence.Comment{}},
		{
			name:    "пустая пачка при непустом next → ErrUnavailable",
			batches: singleBatch(jsonResponse(http.StatusOK, `{"results":[],"_links":{"next":"/x"}}`)),
			wantErr: confluence.ErrUnavailable,
		},
		{name: "404", batches: singleBatch(jsonResponse(http.StatusNotFound, `{"message":"`+bodyMarker+`"}`)), wantErr: confluence.ErrNotFound},
		{name: "200 не JSON", batches: singleBatch(markedHTMLResponse(http.StatusOK)), wantErr: confluence.ErrUnavailable},
		{
			name: "ошибка на второй пачке",
			batches: []listedBatch{
				{start: 0, response: jsonResponse(http.StatusOK, `{"results":[`+rootCommentJSON+`],"_links":{"next":"/x"}}`)},
				{start: 1, response: markedHTMLResponse(http.StatusBadGateway)},
			},
			wantErr: confluence.ErrUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newHarness(t, baseURL)
			calls := make([]any, 0, len(tt.batches))
			for _, batch := range tt.batches {
				calls = append(calls, expectGet(t, harness.transport, commentsURL(batch.start)).Return(batch.response, nil))
			}
			gomock.InOrder(calls...)

			got, err := harness.client.ListComments(t.Context(), "101")

			require.Equal(t, tt.want, got)
			requireSentinel(t, tt.wantErr, err)
			requireNoResponseFragment(t, harness, err)
		})
	}
}

func TestListCommentsStopsAfterTooManyBatches(t *testing.T) {
	const maxListBatches = 20
	harness := newHarness(t, baseURL)
	calls := make([]any, 0, maxListBatches)
	for batch := range maxListBatches {
		calls = append(calls, expectGet(t, harness.transport, commentsURL(batch)).Return(jsonResponse(http.StatusOK, `{"results":[`+rootCommentJSON+`],"_links":{"next":"/x"}}`), nil))
	}
	gomock.InOrder(calls...)

	_, err := harness.client.ListComments(t.Context(), "101")

	requireSentinel(t, confluence.ErrUnavailable, err)
	require.NotEmpty(t, harness.logs.String())
}
