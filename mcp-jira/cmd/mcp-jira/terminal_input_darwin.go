package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func enterHiddenLineInput(fd int) (restoreInput func() error, err error) {
	termios, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, fmt.Errorf("read terminal settings: %w", err)
	}
	settingsBeforeInput := *termios
	hideEcho(termios)
	forceLineInput(termios)
	keepCtrlCSignal(termios)
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, termios); err != nil {
		return nil, fmt.Errorf("set hidden line input: %w", err)
	}
	restoreInput = func() error {
		if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &settingsBeforeInput); err != nil {
			return fmt.Errorf("restore terminal settings: %w", err)
		}
		return nil
	}
	return restoreInput, nil
}

func hideEcho(termios *unix.Termios) {
	termios.Lflag &^= unix.ECHO
}

func forceLineInput(termios *unix.Termios) {
	termios.Lflag |= unix.ICANON
	termios.Iflag |= unix.ICRNL
}

func keepCtrlCSignal(termios *unix.Termios) {
	termios.Lflag |= unix.ISIG
}
