// Package tui is L80's interactive console: a full-screen terminal UI built
// on the standard library alone.
//
// The program follows The Elm Architecture as Bubble Tea implements it (MIT,
// Copyright (c) 2020-2026 Charmbracelet, Inc.; see THIRD_PARTY_NOTICES.md):
// a Model handles one message at a time in Update and draws itself in View;
// side effects run as Cmds on their own goroutines and report back with a
// message.
package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/render"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/term"
)

// Msg is anything a model reacts to.
type Msg any

// Cmd runs off the event loop and returns a message (or nil).
type Cmd func() Msg

// Model is one screen tree.
type Model interface {
	Init() Cmd
	Update(Msg) (Model, Cmd)
	View(w, h int) []string
}

type (
	// KeyMsg, MouseMsg and PasteMsg carry input.
	KeyMsg   = input.Key
	MouseMsg = input.Mouse
	PasteMsg = input.Paste
	// SizeMsg is sent at start and on every resize.
	SizeMsg struct{ W, H int }
	quitMsg struct{}
	batch   []Cmd
	// ExecMsg asks the program to hand the terminal to Run (an interactive
	// child such as `vast ssh` or the dashboard's own UI) and then send
	// Done(err).
	ExecMsg struct {
		Run  func() error
		Done func(error) Msg
	}
)

// Quit ends the program.
func Quit() Msg { return quitMsg{} }

// Batch runs commands concurrently.
func Batch(cmds ...Cmd) Cmd {
	var keep []Cmd
	for _, c := range cmds {
		if c != nil {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		return nil
	}
	return func() Msg { return batch(keep) }
}

// Tick sends fn(t) after d.
func Tick(d time.Duration, fn func(time.Time) Msg) Cmd {
	return func() Msg { return fn(<-time.After(d)) }
}

// Program runs a model on the terminal.
type Program struct {
	model   Model
	msgs    chan Msg
	profile style.Profile

	tty      *os.File
	fd       uintptr // tty's descriptor, taken without p.fd (see Run)
	out      io.Writer
	state    *term.State
	renderer *render.Renderer
	w, h     int

	pauseMu   sync.Mutex
	pausable  bool
	paused    chan struct{} // closed by the reader when it has stopped
	resume    chan struct{}
	restoreMu sync.Once
}

// NewProgram prepares a program; Run starts it.
func NewProgram(m Model) *Program {
	return &Program{model: m, msgs: make(chan Msg, 256), profile: style.DetectProfile(nil)}
}

// Profile is the colour profile the program detected.
func (p *Program) Profile() style.Profile { return p.profile }

// Send delivers a message from outside the loop (subprocess output, timers).
// It never blocks the caller for long: if the queue is full the message waits
// on its own goroutine.
func (p *Program) Send(m Msg) {
	select {
	case p.msgs <- m:
	default:
		go func() { p.msgs <- m }()
	}
}

// ErrNoTTY is returned when there is no terminal to draw on.
var ErrNoTTY = errors.New("the interactive console needs a terminal")

// Run takes over the terminal until the model quits.
func (p *Program) Run() (err error) {
	if !term.IsTerminal(os.Stdout.Fd()) {
		return ErrNoTTY
	}
	tty, terr := term.OpenTTY()
	if terr != nil {
		return fmt.Errorf("%w: %v", ErrNoTTY, terr)
	}
	p.tty, p.out = tty, os.Stdout
	defer tty.Close()
	// A tty opened through os.OpenFile is registered with the poller, so a
	// read deadline can pause input while a child owns the terminal. That
	// only holds while the file stays non-blocking, and (*os.File).Fd puts
	// it back into blocking mode, so the descriptor is taken through
	// SyscallConn instead and tty.Fd is never called.
	rc, cerr := tty.SyscallConn()
	if cerr != nil {
		return cerr
	}
	rc.Control(func(fd uintptr) { p.fd = fd })
	p.pausable = tty.SetReadDeadline(time.Time{}) == nil

	if p.state, err = term.MakeRaw(p.fd); err != nil {
		return err
	}
	defer p.restore()
	defer func() {
		if r := recover(); r != nil {
			p.restore()
			stack := strings.ReplaceAll(string(debug.Stack()), "\n", "\r\n")
			fmt.Fprintf(os.Stderr, "L80 tui crashed: %v\r\n%s", r, stack)
			err = fmt.Errorf("tui: %v", r)
		}
	}()
	p.enter()

	p.w, p.h, _ = term.Size(p.fd)
	if p.w == 0 {
		p.w, p.h = 80, 24
	}
	p.renderer = render.New(p.out, p.w, p.h)

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, watchedSignals...)
	defer signal.Stop(sigs)
	go func() {
		for s := range sigs {
			if isResize(s) {
				if w, h, err := term.Size(p.fd); err == nil {
					p.Send(SizeMsg{w, h})
				}
				continue
			}
			p.Send(quitMsg{})
		}
	}()
	go p.readInput()

	p.Send(SizeMsg{p.w, p.h})
	p.run(p.model.Init())

	frame := time.NewTicker(time.Second / 30)
	defer frame.Stop()
	dirty := true
	for {
		select {
		case m := <-p.msgs:
			switch v := m.(type) {
			case quitMsg:
				return nil
			case batch:
				for _, c := range v {
					p.run(c)
				}
				continue
			case SizeMsg:
				p.w, p.h = v.W, v.H
				p.renderer.Resize(v.W, v.H)
			case ExecMsg:
				p.exec(v)
				dirty = true
				continue
			}
			var cmd Cmd
			p.model, cmd = p.model.Update(m)
			p.run(cmd)
			dirty = true
		case <-frame.C:
			if dirty {
				p.renderer.Frame(p.model.View(p.w, p.h))
				dirty = false
			}
		}
	}
}

func (p *Program) enter() {
	io.WriteString(p.out, term.AltScreenOn+term.HideCursor+term.MouseOn+term.PasteOn)
}

func (p *Program) leave() {
	io.WriteString(p.out, term.PasteOff+term.MouseOff+term.ResetAttributes+term.ShowCursor+term.AltScreenOff)
}

// restore leaves the alternate screen and restores the terminal mode. Safe
// to call more than once.
func (p *Program) restore() {
	p.restoreMu.Do(func() {
		p.leave()
		if p.tty != nil {
			term.Restore(p.fd, p.state)
		}
	})
}

// run executes a command on its own goroutine; a panic in it is reported as
// an error message rather than taking the terminal down with it.
func (p *Program) run(c Cmd) {
	if c == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				p.Send(ErrorMsg{Err: fmt.Errorf("internal error: %v", r)})
			}
		}()
		if m := c(); m != nil {
			p.Send(m)
		}
	}()
}

// ErrorMsg reports a failure to the model.
type ErrorMsg struct{ Err error }

func (p *Program) readInput() {
	events := make(chan input.Event, 64)
	go func() {
		for ev := range events {
			p.Send(ev)
		}
	}()
	for {
		err := input.Read(p.tty, events)
		p.pauseMu.Lock()
		paused, resume := p.paused, p.resume
		p.pauseMu.Unlock()
		if paused != nil && errors.Is(err, os.ErrDeadlineExceeded) {
			close(paused)
			<-resume
			continue
		}
		return
	}
}

// exec hands the terminal to v.Run: input reading stops, the screen and mode
// are restored, Run executes, then the program takes the terminal back.
func (p *Program) exec(v ExecMsg) {
	if !p.pausable {
		p.Send(v.Done(errors.New("this terminal cannot be handed to another program from the console; run the command in another terminal")))
		return
	}
	p.pauseMu.Lock()
	p.paused, p.resume = make(chan struct{}), make(chan struct{})
	paused, resume := p.paused, p.resume
	p.pauseMu.Unlock()
	p.tty.SetReadDeadline(time.Now())
	<-paused

	p.leave()
	term.Restore(p.fd, p.state)
	err := v.Run()
	if st, rerr := term.MakeRaw(p.fd); rerr == nil {
		p.state = st
	}
	p.enter()
	if w, h, serr := term.Size(p.fd); serr == nil {
		p.w, p.h = w, h
	}
	p.renderer.Resize(p.w, p.h)

	p.pauseMu.Lock()
	p.paused, p.resume = nil, nil
	p.pauseMu.Unlock()
	p.tty.SetReadDeadline(time.Time{})
	close(resume)
	p.Send(SizeMsg{p.w, p.h})
	if v.Done != nil {
		p.Send(v.Done(err))
	}
}
