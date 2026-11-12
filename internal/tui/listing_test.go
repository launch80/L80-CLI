package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui/input"
)

// The publish dialog asks about the Directory with Cancel focused, and its
// shortcuts never make "p" (habit for publish) list a page.
func TestConfirmPublishAsksAboutTheDirectory(t *testing.T) {
	c := newTestConsole(t)
	t.Setenv("L80_PUBLISH_PUBLIC", "")
	for _, tc := range []struct {
		key        rune
		wantCalled bool
		wantPublic bool
	}{{'l', true, true}, {'p', true, false}, {'c', false, false}} {
		called, public := false, false
		c.confirmPublish("Publish?", "bench.betterbench.v1", func(p bool) Cmd { called, public = true, p; return nil })
		if c.dialog == nil || c.dialog.Buttons[c.dialog.Choice] != "Cancel" {
			t.Fatalf("Cancel must be focused: %+v", c.dialog)
		}
		feed(c, key(tc.key))
		if called != tc.wantCalled || public != tc.wantPublic {
			t.Errorf("key %q: called %v public %v", tc.key, called, public)
		}
	}
	// Esc cancels.
	called := false
	c.confirmPublish("Publish?", "scan.pwdet.v1", func(bool) Cmd { called = true; return nil })
	feed(c, code(input.Esc))
	if called {
		t.Error("Esc published")
	}
	// A template the Directory does not take keeps the plain dialog.
	c.confirmPublish("Publish?", "generic.report.v1", func(bool) Cmd { return nil })
	if strings.Join(c.dialog.Buttons, "|") != "Publish|Cancel" || strings.Contains(c.dialog.Body, "Directory") {
		t.Errorf("generic dialog: %+v", c.dialog)
	}
	feed(c, code(input.Esc))
	// publish.public answers the question.
	for mode, want := range map[string]string{"always": "List & publish|Cancel", "never": "Publish unlisted|Cancel"} {
		if err := config.Set(config.KeyPublishPublic, mode); err != nil {
			t.Fatal(err)
		}
		c.confirmPublish("Publish?", "bench.betterbench.v1", func(bool) Cmd { return nil })
		if got := strings.Join(c.dialog.Buttons, "|"); got != want || c.dialog.Buttons[c.dialog.Choice] != "Cancel" {
			t.Errorf("%s: buttons %q, focus %d", mode, got, c.dialog.Choice)
		}
		feed(c, code(input.Esc))
	}
}

// History shows which pages are listed, and l lists or unlists one after asking.
func TestHistoryListsAndUnlistsAPage(t *testing.T) {
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/visibility"):
			var in struct {
				Listed bool `json:"listed"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			puts = append(puts, r.URL.Path)
			if in.Listed {
				w.Write([]byte(`{"ok":true,"data":{"guid":"g0","url":"https://launch80.com/a/g0","listed":true}}`))
			} else {
				w.Write([]byte(`{"ok":true,"data":{"guid":"g0","url":"https://launch80.com/a/g0","listed":false}}`))
			}
		case r.URL.Path == "/api/v1/artifacts":
			w.Write([]byte(`{"ok":true,"data":{"key_id":"k1","artifacts":[` +
				`{"guid":"g0","url":"https://launch80.com/a/g0","template_id":"bench.betterbench.v1","title":"B65 run","created_at":"2026-11-04T21:01:00Z","trust_tier":"unverified","listed":false},` +
				`{"guid":"g9","url":"https://launch80.com/a/g9","template_id":"generic.report.v1","title":"Notes","created_at":"2026-11-03T21:01:00Z","trust_tier":"unverified"}]}}`))
		default:
			w.Write([]byte(`{"ok":true,"data":{}}`))
		}
	}))
	defer srv.Close()
	c := newTestConsole(t)
	c.d.Config = config.Config{BaseURL: srv.URL, Token: "L80_k1_secret", KeyID: "k1"}
	c.d.Client = api.NewClient(srv.URL, c.d.Config.Token, "test")
	feed(c, SizeMsg{120, 44})
	jumpTo(t, c, scrHistory)
	if v := screen(t, c); !strings.Contains(v, "Dir") || !strings.Contains(v, "l list / unlist") {
		t.Fatalf("history:\n%s", v)
	}

	feed(c, key('l'))
	if c.dialog == nil || c.dialog.Title != "List in the Directory" || c.dialog.Buttons[c.dialog.Choice] != "Cancel" {
		t.Fatalf("l must ask, Cancel focused: %+v", c.dialog)
	}
	feed(c, code(input.Esc))
	if len(puts) != 0 {
		t.Fatal("listed without a yes")
	}
	feed(c, key('l'))
	feed(c, code(input.Left))
	feed(c, code(input.Enter))
	if len(puts) != 1 || puts[0] != "/api/v1/artifacts/g0/visibility" || !c.hist.entries[0].Listed {
		t.Fatalf("puts %v; flash %q", puts, c.flash)
	}
	if !strings.Contains(c.flash, "listed in the Directory") || !strings.Contains(screen(t, c), "●") {
		t.Errorf("flash %q\n%s", c.flash, screen(t, c))
	}
	// And back.
	feed(c, key('l'))
	if c.dialog.Title != "Unlist" {
		t.Fatalf("second l must offer to unlist: %+v", c.dialog)
	}
	feed(c, code(input.Left))
	feed(c, code(input.Enter))
	if len(puts) != 2 || c.hist.entries[0].Listed || !strings.Contains(c.flash, "unlisted") {
		t.Errorf("puts %v; flash %q", puts, c.flash)
	}
	// A generic report cannot be listed.
	feed(c, code(input.Down))
	feed(c, key('l'))
	if c.dialog != nil || !strings.Contains(c.flash, "only benchmark and scan pages") {
		t.Errorf("generic: dialog %+v flash %q", c.dialog, c.flash)
	}
}
