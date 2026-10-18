package input

import (
	"io"
	"strings"
	"testing"
	"time"
)

func keys(t *testing.T, in string, final bool) []string {
	t.Helper()
	evs, _ := Parse([]byte(in), final)
	var out []string
	for _, e := range evs {
		switch v := e.(type) {
		case Key:
			out = append(out, v.String())
		case Paste:
			out = append(out, "paste:"+v.Text)
		case Mouse:
			out = append(out, "mouse")
		}
	}
	return out
}

func TestDecodeTable(t *testing.T) {
	cases := map[string]string{
		"q":                      "q",
		"\x03":                   "ctrl+c",
		"\r":                     "enter",
		"\t":                     "tab",
		"\x7f":                   "backspace",
		"\x1b[A":                 "up",
		"\x1bOB":                 "down",
		"\x1b[1;5C":              "ctrl+right",
		"\x1b[1;3D":              "alt+left",
		"\x1b[5~":                "pgup",
		"\x1b[6;2~":              "shift+pgdown",
		"\x1b[3~":                "delete",
		"\x1b[H":                 "home",
		"\x1b[4~":                "end",
		"\x1bOP":                 "f1",
		"\x1b[24~":               "f12",
		"\x1b[Z":                 "shift+tab",
		"\x1bx":                  "alt+x",
		"é":                      "é",
		"日本":                     "日 本",
		"\x1b[200~a\nb\x1b[201~": "paste:a\nb",
		"ab\x1b[B":               "a b down",
		"\x1b[I":                 "",
	}
	for in, want := range cases {
		if got := strings.Join(keys(t, in, false), " "); got != want {
			t.Errorf("%q => %q, want %q", in, got, want)
		}
	}
}

func TestIncompleteSequencesWait(t *testing.T) {
	for _, in := range []string{"\x1b", "\x1b[", "\x1b[1;5", "\xe6\x97", "\x1b[200~partial"} {
		evs, n := Parse([]byte(in), false)
		if len(evs) != 0 || n != 0 {
			t.Errorf("%q: should wait, got %v consumed %d", in, evs, n)
		}
	}
	if got := keys(t, "\x1b", true); len(got) != 1 || got[0] != "esc" {
		t.Errorf("final lone ESC = %v", got)
	}
}

func TestSGRMouse(t *testing.T) {
	evs, _ := Parse([]byte("\x1b[<0;10;5M\x1b[<64;1;1M\x1b[<65;1;1M\x1b[<0;10;5m"), false)
	if len(evs) != 4 {
		t.Fatalf("got %d events", len(evs))
	}
	m := evs[0].(Mouse)
	if m.X != 9 || m.Y != 4 || m.Button != ButtonLeft || m.Release {
		t.Errorf("press = %+v", m)
	}
	if evs[1].(Mouse).Button != WheelUp || evs[2].(Mouse).Button != WheelDown || !evs[3].(Mouse).Release {
		t.Error("wheel/release wrong")
	}
}

func TestReadFlushesLoneEscAfterTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	out := make(chan Event, 8)
	go Read(pr, out)
	pw.Write([]byte("\x1b"))
	select {
	case e := <-out:
		if e.(Key).Code != Esc {
			t.Fatalf("got %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("lone ESC never delivered")
	}
	pw.Write([]byte("\x1b[A"))
	if e := <-out; e.(Key).Code != Up {
		t.Fatalf("got %v", e)
	}
	pw.Close()
}
