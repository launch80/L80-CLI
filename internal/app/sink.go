// Package app holds command bodies shared by the CLI and the TUI.
//
// A command here takes a request, does the work, and reports progress to a
// Sink. It never parses flags and never decides how a result is printed: the
// CLI prints to stdout/stderr, the TUI draws a pane, from the same calls.
package app

import (
	"fmt"
	"io"
)

// Kind classifies a progress note.
type Kind int

const (
	// Step is ordinary progress ("running BetterBench via PATH").
	Step Kind = iota
	// Warn is a caveat the user should read before trusting the result.
	Warn
	// Done is a success line.
	Done
)

// Sink receives a command's progress. Output is where a child tool's own
// stdout/stderr stream; Note is for L80's lines about the work.
type Sink interface {
	Output() io.Writer
	Note(k Kind, format string, args ...any)
}

// WriterSink is the CLI's sink: tool output and notes both go to one writer
// (stderr), notes indented the way output.Detailf indents them.
type WriterSink struct{ W io.Writer }

func (s WriterSink) Output() io.Writer { return s.W }

func (s WriterSink) Note(k Kind, format string, args ...any) {
	prefix := "  "
	switch k {
	case Warn:
		prefix = "  warning: "
	case Done:
		prefix = ""
	}
	fmt.Fprintf(s.W, prefix+format+"\n", args...)
}

// Discard is a sink that drops everything.
type Discard struct{}

func (Discard) Output() io.Writer         { return io.Discard }
func (Discard) Note(Kind, string, ...any) {}
