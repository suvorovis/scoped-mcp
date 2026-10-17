package guard

import (
	"context"

	"github.com/gotd/td/bin"
	gotd "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func NewFolderReader() gotd.MiddlewareFunc {
	return func(nextInvoker tg.Invoker) gotd.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			if !isFolderReaderRequest(input) {
				return forbidden(input)
			}
			return nextInvoker.Invoke(ctx, input, output)
		}
	}
}

func isFolderReaderRequest(input bin.Encoder) bool {
	switch input.(type) {
	case *tg.HelpGetConfigRequest, *tg.MessagesGetDialogFiltersRequest:
		return true
	default:
		return false
	}
}
