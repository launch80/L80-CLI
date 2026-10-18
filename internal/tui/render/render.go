// Package render draws frames to the terminal's alternate screen, writing
// only the lines that changed since the last frame.
//
// It is the line-diff design of Bubble Tea's v1 standard renderer (MIT,
// Copyright (c) 2020-2026 Charmbracelet, Inc.; see THIRD_PARTY_NOTICES.md):
// never clear-then-redraw, cut lines instead of letting them wrap, and send
// a whole frame in one write, bracketed as synchronized output where the
// terminal supports it.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/term"
)

// Renderer holds the last frame drawn.
type Renderer struct {
	out  io.Writer
	w, h int
	last []string
}

func New(out io.Writer, w, h int) *Renderer { return &Renderer{out: out, w: w, h: h} }

// Resize sets a new size and forces the next frame to repaint everything.
func (r *Renderer) Resize(w, h int) {
	r.w, r.h = w, h
	r.Invalidate()
}

// Invalidate forgets the last frame, e.g. after another program used the
// screen.
func (r *Renderer) Invalidate() { r.last = nil }

// Frame renders lines (at most h of them, each cut to w cells) and returns
// what was written, for tests.
func (r *Renderer) Frame(lines []string) (string, error) {
	var b strings.Builder
	b.WriteString(term.SyncBegin)
	if r.last == nil {
		b.WriteString(term.ResetAttributes + term.Home + term.ClearScreen)
	}
	next := make([]string, r.h)
	for i := 0; i < r.h; i++ {
		line := ""
		if i < len(lines) {
			line = style.Truncate(lines[i], r.w, "")
		}
		next[i] = line
		if r.last != nil && i < len(r.last) && r.last[i] == line {
			continue
		}
		fmt.Fprintf(&b, "\x1b[%d;1H%s%s", i+1, line, term.ResetAttributes)
		if style.Width(line) < r.w {
			b.WriteString(term.EraseLineRight)
		}
	}
	b.WriteString(term.SyncEnd)
	r.last = next
	s := b.String()
	_, err := io.WriteString(r.out, s)
	return s, err
}
