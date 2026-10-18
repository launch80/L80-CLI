package term

import (
	"os"
	"testing"
)

func TestPipeIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if IsTerminal(r.Fd()) {
		t.Error("a pipe is not a terminal")
	}
	if _, err := MakeRaw(r.Fd()); err == nil {
		t.Error("raw mode on a pipe must fail")
	}
	if _, _, err := Size(r.Fd()); err == nil {
		t.Error("a pipe has no size")
	}
}
