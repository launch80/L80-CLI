// Command ttydiag is a throwaway diagnostic for the L80 console's terminal
// handoff. It reproduces, step by step, what internal/tui does at startup:
// open /dev/tty, take the raw descriptor through SyscallConn, set a read
// deadline, and check that the deadline actually fires.
//
// Run it in the same terminal where "L80 tui > dashboard" fails:
//
//	curl -sL -o /tmp/ttydiag https://raw.githubusercontent.com/launch80/L80-CLI/ttydiag/diag_$(uname -m)
//	chmod +x /tmp/ttydiag && /tmp/ttydiag
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
)

func main() {
	fmt.Printf("go %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Println("running in a terminal; every step mirrors what L80 tui does")

	st, err := os.Stat("/dev/tty")
	if err != nil {
		fmt.Println("stat /dev/tty:", err)
		return
	}
	typ := st.Mode() & os.ModeType
	if typ == os.ModeCharDevice {
		fmt.Println("/dev/tty: character device, as expected")
	} else {
		fmt.Printf("/dev/tty: unexpected type bits %o\n", typ)
	}

	check("O_RDONLY", os.O_RDONLY)
	check("O_RDWR", os.O_RDWR)
}

// check opens /dev/tty the given way and walks through the L80 sequence.
func check(label string, flag int) {
	fmt.Printf("\n--- %s ---\n", label)
	f, err := os.OpenFile("/dev/tty", flag, 0)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer f.Close()

	// L80 takes the descriptor through SyscallConn so the file stays
	// registered with Go's poller; the deadline call is the pausable check.
	rc, err := f.SyscallConn()
	if err != nil {
		fmt.Println("SyscallConn:", err)
		return
	}
	var fd uintptr
	rc.Control(func(d uintptr) { fd = d })
	fmt.Println("fd via SyscallConn:", fd)

	if err := f.SetReadDeadline(time.Time{}); err != nil {
		fmt.Println("SetReadDeadline(zero):", err)
		fmt.Println("=> L80 would say: this terminal cannot be handed to another program")
		return
	}
	fmt.Println("SetReadDeadline(zero): nil")

	// A nil return is only useful if the deadline actually fires. Ask the
	// file for a byte that will never come, 200 ms from now.
	f.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := f.Read(buf)
		done <- err
	}()
	var readErr error
	select {
	case readErr = <-done:
	case <-time.After(3 * time.Second):
		readErr = errors.New("read blocked past the 3s watchdog (deadline never fired)")
	}
	switch {
	case errors.Is(readErr, os.ErrDeadlineExceeded):
		fmt.Println("deadline fired as expected (i/o timeout) => handoff would work")
	case readErr == nil:
		fmt.Println("read returned a byte immediately (input was pending?) => inconclusive, run again with a quiet terminal")
	default:
		fmt.Println("read:", readErr, "=> handoff would NOT work")
	}
}
