package telegram

import (
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
	"github.com/stretchr/testify/require"
)

func TestMapError(t *testing.T) {
	networkErr := errors.New("network down")
	peerInvalidErr := tgerr.New(400, "PEER_ID_INVALID")
	tests := []struct {
		name      string
		err       error
		wantIs    error
		wantFlood time.Duration
	}{
		{name: "nil", err: nil},
		{name: "FLOOD_WAIT_30", err: tgerr.New(420, "FLOOD_WAIT_30"), wantFlood: 30 * time.Second},
		{name: "FLOOD_PREMIUM_WAIT_5", err: tgerr.New(420, "FLOOD_PREMIUM_WAIT_5"), wantFlood: 5 * time.Second},
		{name: "AUTH_KEY_UNREGISTERED", err: tgerr.New(401, "AUTH_KEY_UNREGISTERED"), wantIs: ErrSessionRevoked},
		{name: "SESSION_REVOKED", err: tgerr.New(401, "SESSION_REVOKED"), wantIs: ErrSessionRevoked},
		{name: "CHANNEL_FORUM_MISSING", err: tgerr.New(400, "CHANNEL_FORUM_MISSING"), wantIs: ErrNotForum},
		{name: "другая rpc-ошибка проходит как есть", err: peerInvalidErr, wantIs: peerInvalidErr},
		{name: "не rpc-ошибка проходит как есть", err: networkErr, wantIs: networkErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.err)
			if tt.wantFlood != 0 {
				var floodErr *FloodWaitError
				require.ErrorAs(t, got, &floodErr)
				require.Equal(t, tt.wantFlood, floodErr.Wait)
				return
			}
			if tt.wantIs == nil {
				require.NoError(t, got)
				return
			}
			require.ErrorIs(t, got, tt.wantIs)
		})
	}
}
