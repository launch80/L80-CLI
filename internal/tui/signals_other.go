//go:build !unix

package tui

import "os"

var watchedSignals = []os.Signal{os.Interrupt}

func isResize(os.Signal) bool { return false }
