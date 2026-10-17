//go:build !darwin

package main

import "errors"

var errHiddenInputUnsupported = errors.New("token: hiding terminal input is supported only on macOS, pipe the token into stdin instead")

func enterHiddenLineInput(int) (restoreInput func() error, err error) {
	return nil, errHiddenInputUnsupported
}
