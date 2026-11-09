package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
	"github.com/launch80/L80-CLI/internal/qualitybench"
	"github.com/launch80/L80-CLI/internal/tui/term"
)

// These are swapped in tests.
var (
	// stdinIsTTY reports whether L80 can ask a question and get an answer.
	stdinIsTTY = func() bool { return term.IsTerminal(os.Stdin.Fd()) }
	// stderrIsTTY reports whether progress can be one rewritten line.
	stderrIsTTY           = func() bool { return term.IsTerminal(os.Stderr.Fd()) }
	promptIn    io.Reader = os.Stdin
	// estimateQuality runs the tool's estimate (app.EstimateQuality).
	estimateQuality func(ctx context.Context, o qualitybench.RunOptions) qualitybench.Estimate
)

// printEstimate writes the estimate the way the gate shows it.
func printEstimate(w io.Writer, e qualitybench.Estimate) {
	output.Detailf(w, "estimate: %s", e.Headline())
	if line := e.TaskLine(); line != "" {
		output.Detailf(w, "  %s", line)
	}
	if e.SetupMin > 0 {
		output.Detailf(w, "  first run: + about %s of setup (harness environments, datasets)", qualitybench.Minutes(e.SetupMin))
	}
	if e.Warning != "" {
		output.Detailf(w, "  (%s)", e.Warning)
	}
}

// gateDecision is what the gate decided and why.
type gateDecision struct {
	Run    bool
	Asked  bool
	Reason string
}

// qualityGate decides whether to start: --yes and the qualitybench.confirm
// setting skip the question; without a terminal to ask on, L80 proceeds
// (scripts and agents read the estimate above and use --estimate-only or
// --max-minutes for a limit).
func qualityGate(w io.Writer, e qualitybench.Estimate, yes bool) gateDecision {
	if yes {
		return gateDecision{Run: true, Reason: "--yes"}
	}
	c, src, err := config.QualityConfirm()
	if err != nil {
		output.Detailf(w, "warning: qualitybench.confirm (%s): %v; asking", src, err)
	}
	if !c.Ask(e.Upper()) {
		return gateDecision{Run: true, Reason: "qualitybench.confirm is " + c.String()}
	}
	if !stdinIsTTY() {
		return gateDecision{Run: true, Reason: "no terminal to ask on"}
	}
	fmt.Fprintf(w, "Start QualityBench (%s)? [y/N] ", qualitybench.Minutes(e.Total()))
	line, _ := bufio.NewReader(promptIn).ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans == "y" || ans == "yes" {
		return gateDecision{Run: true, Asked: true}
	}
	return gateDecision{Asked: true, Reason: "not started"}
}

// parseDeadline reads --deadline as the tool does: RFC 3339 or a duration.
func parseDeadline(s string, now time.Time) time.Time {
	if s == "" {
		return time.Time{}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(d)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// statusWriter turns the tool's qb:progress lines into one status line that
// is rewritten in place on a terminal; every other line passes through above it.
type statusWriter struct {
	mu    sync.Mutex
	out   io.Writer
	tr    *qualitybench.Tracker
	buf   []byte
	shown bool
	last  time.Time
	now   func() time.Time
}

func newStatusWriter(out io.Writer, tr *qualitybench.Tracker) *statusWriter {
	return &statusWriter{out: out, tr: tr, now: time.Now}
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, b...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := string(w.buf[:i])
		w.buf = w.buf[i+1:]
		if w.tr.Parse(line) {
			continue
		}
		w.clear()
		fmt.Fprintln(w.out, line)
	}
	w.draw(false)
	return len(b), nil
}

func (w *statusWriter) clear() {
	if w.shown {
		fmt.Fprint(w.out, "\r\033[K")
		w.shown = false
	}
}

func (w *statusWriter) draw(force bool) {
	if !force && w.shown && w.now().Sub(w.last) < 500*time.Millisecond {
		return
	}
	o := w.tr.Overall()
	if o.Current == nil && o.Fraction == 0 {
		return
	}
	fmt.Fprint(w.out, "\r\033[K  "+o.Status())
	w.shown, w.last = true, w.now()
}

// Close ends the status line so later output starts on its own line.
func (w *statusWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 && !w.tr.Parse(string(w.buf)) {
		w.clear()
		fmt.Fprintln(w.out, string(w.buf))
	}
	w.buf = nil
	if w.shown {
		fmt.Fprintln(w.out)
		w.shown = false
	}
}

func init() {
	estimateQuality = func(ctx context.Context, o qualitybench.RunOptions) qualitybench.Estimate {
		return app.EstimateQuality(ctx, o, false)
	}
}
