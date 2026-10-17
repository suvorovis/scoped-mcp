package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func disableTerminalEcho(fd int) (restoreEcho func() error, err error) {
	termios, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, fmt.Errorf("read terminal settings: %w", err)
	}
	settingsBeforeInput := *termios
	termios.Lflag &^= unix.ECHO
	termios.Lflag |= unix.ICANON | unix.ISIG
	termios.Iflag |= unix.ICRNL
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, termios); err != nil {
		return nil, fmt.Errorf("disable terminal echo: %w", err)
	}
	restoreEcho = func() error {
		if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &settingsBeforeInput); err != nil {
			return fmt.Errorf("restore terminal settings: %w", err)
		}
		return nil
	}
	return restoreEcho, nil
}
