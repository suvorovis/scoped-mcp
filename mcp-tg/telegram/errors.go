package telegram

import (
	"errors"
	"fmt"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const unauthorizedCode = 401

var (
	ErrNotForum       = errors.New("telegram: chat is not a forum")
	ErrSessionRevoked = errors.New("telegram: session revoked, run `mcp-tg login`")
	ErrFolderNotFound = errors.New("telegram: folder not found in the account, run `mcp-tg folders` to list folder ids")
	ErrFolderNoChats  = errors.New("telegram: folder has no explicitly added chats (folder categories are ignored)")
)

type FloodWaitError struct {
	Wait time.Duration
}

func (e *FloodWaitError) Error() string {
	return fmt.Sprintf("telegram: flood wait, retry after %s", e.Wait)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if wait, ok := tgerr.AsFloodWait(err); ok {
		return &FloodWaitError{Wait: wait}
	}
	if tgerr.IsCode(err, unauthorizedCode) {
		return fmt.Errorf("%w: %w", ErrSessionRevoked, err)
	}
	if tg.IsChannelForumMissing(err) {
		return fmt.Errorf("%w: %w", ErrNotForum, err)
	}
	return err
}
