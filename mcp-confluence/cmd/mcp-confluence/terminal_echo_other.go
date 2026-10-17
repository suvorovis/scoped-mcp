//go:build !darwin

package main

import "errors"

var errTerminalEchoUnsupported = errors.New("token: hiding terminal input is supported only on macOS, pipe the token into stdin instead")

func disableTerminalEcho(int) (restoreEcho func() error, err error) {
	return nil, errTerminalEchoUnsupported
}
