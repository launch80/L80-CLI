package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/tui/input"
	"github.com/launch80/L80-CLI/internal/tui/style"
	"github.com/launch80/L80-CLI/internal/tui/widgets"
)

// Settings: the token (what `L80 auth status` shows, plus saving and
// removing it), the API base, and `L80 update`.

const scrSettings screenID = "settings"

func init() {
	register(screenDef{id: scrSettings, label: "Settings", side: true, blurb: "Your token, the launch80 server, and updates.",
		get: func(c *Console) screenModel { return settings(c) }, open: func(c *Console) Cmd { settings(c).ensure(); return nil }})
}

type settingsScreen struct {
	menu     widgets.List
	typing   bool // the token input has the keyboard
	token    widgets.TextInput
	msg      string
	bad      bool
	checking bool
	check    *updateCheck
	checkErr string
	job      *job
}

// updateCheck is the part of `L80 update --check --json` the screen shows.
type updateCheck struct {
	Current         string   `json:"current"`
	Latest          string   `json:"latest"`
	UpdateAvailable bool     `json:"update_available"`
	Notes           string   `json:"notes"`
	Actions         []string `json:"actions"`
	UpToDate        bool     `json:"up_to_date"`
}

type updateChecked struct {
	res *updateCheck
	err string
}

func settings(c *Console) *settingsScreen { return stateOf[settingsScreen](c, scrSettings) }

func (s *settingsScreen) ensure() {
	if len(s.menu.Items) > 0 {
		return
	}
	s.token = widgets.TextInput{Mask: true, Placeholder: "paste a token: L80_…"}
	s.menu = widgets.List{Items: []widgets.Item{
		{Title: "Log in", Desc: "Save a launch80 token to the credentials file", Value: "login"},
		{Title: "Log out", Desc: "Remove the saved token", Value: "logout"},
		{Title: "Check for updates", Desc: "What `L80 update --check` finds, with the release notes", Value: "check"},
		{Title: "Update L80", Desc: "Install the latest release and refresh tools and skills (`L80 update`)", Value: "update"},
	}}
}

// login checks a token's shape, saves it where config.Load finds it, and
// switches the console to it. The message never contains the token.
func (c *Console) login(tok string) (string, bool) {
	tok = strings.TrimSpace(tok)
	if config.KeyIDOf(tok) == "" {
		return "That does not look like a launch80 token (L80_<key>_<secret>).", false
	}
	if err := config.SaveCredentials(tok); err != nil {
		return "Could not save it: " + err.Error(), false
	}
	cfg := &c.d.Config
	cfg.Token, cfg.KeyID, cfg.TokenSource, cfg.CredPath = tok, config.KeyIDOf(tok), config.SourceFile, config.CredentialsPath()
	c.d.Client = api.NewClient(cfg.BaseURL, tok, c.d.Version)
	return "Saved to " + config.CredentialsPath() + " (readable only by you).", true
}

// logout removes the saved token. A token from L80_TOKEN is the shell's to unset.
func (c *Console) logout() (string, bool) {
	cfg := &c.d.Config
	if cfg.TokenSource == config.SourceEnv {
		return "The token comes from L80_TOKEN. Unset it in your shell to log out.", false
	}
	if err := config.RemoveCredentials(); err != nil {
		return "Could not remove it: " + err.Error(), false
	}
	cfg.Token, cfg.KeyID, cfg.TokenSource = "", "", config.SourceNone
	c.d.Client = api.NewClient(cfg.BaseURL, "", c.d.Version)
	return "Logged out. Tools still work; publishing needs a token again.", true
}

// apiBaseSource says where the API base came from, in the order config.Load applies.
func apiBaseSource(base string) string {
	env := strings.TrimRight(os.Getenv("L80_API_BASE"), "/")
	switch {
	case base == config.DefaultBaseURL && env == "":
		return "default"
	case env != "" && (env == base || (env == "https://launch80.com" && base == config.DefaultBaseURL)):
		return "L80_API_BASE"
	}
	return "--api-base"
}

func (s *settingsScreen) runCheck(c *Console) Cmd {
	if c.d.Update == nil {
		s.checkErr = "updates are not available in this build"
		return nil
	}
	s.checking, s.checkErr = true, ""
	return func() Msg {
		var out, errOut bytes.Buffer
		c.d.Update([]string{"--check", "--json"}, &out, &errOut)
		var res updateCheck
		if err := json.Unmarshal(out.Bytes(), &res); err != nil {
			msg := strings.TrimSpace(errOut.String())
			if msg == "" {
				msg = "could not read what `L80 update --check` returned"
			}
			return updateChecked{err: msg}
		}
		return updateChecked{res: &res}
	}
}

func (s *settingsScreen) runUpdate(c *Console) Cmd {
	switch {
	case s.job != nil && s.job.running:
		return nil
	case s.check == nil:
		c.setFlash("check for updates first", true)
		return nil
	case s.check.UpToDate:
		c.setFlash("L80 "+s.check.Current+" is up to date", false)
		return nil
	}
	body := "This refreshes the pinned tools and the skills L80 installed."
	if s.check.UpdateAvailable {
		body = "This replaces the L80 binary with " + s.check.Latest + " and refreshes the pinned tools and installed skills.\n\nRestart the console afterwards to use the new version."
	}
	c.confirm(&widgets.Dialog{Title: "Update L80", Body: body, Buttons: []string{"Update", "Cancel"}, Choice: 1}, func(ch int) Cmd {
		if ch != 0 {
			return nil
		}
		s.job = newJob("L80 update")
		return c.start(s.job, func(ctx context.Context, out io.Writer) (any, int, error) {
			return nil, c.d.Update(nil, out, out), nil
		})
	})
	return nil
}

func (s *settingsScreen) update(c *Console, m Msg) Cmd {
	s.ensure()
	switch v := m.(type) {
	case updateChecked:
		s.checking, s.check, s.checkErr = false, v.res, v.err
	case jobDone:
		if v.j == s.job && v.code == 0 && v.err == nil {
			return s.runCheck(c)
		}
	case PasteMsg:
		if s.typing {
			s.token.HandlePaste(v)
		}
	case KeyMsg:
		if s.typing {
			switch v.Code {
			case input.Esc:
				s.typing = false
				s.token.Set("")
			case input.Enter:
				if strings.TrimSpace(s.token.String()) == "" {
					s.typing = false
					return nil
				}
				msg, ok := c.login(s.token.String())
				s.msg, s.bad = msg, !ok
				if ok {
					s.typing = false
					s.token.Set("")
				}
			default:
				s.token.HandleKey(v)
			}
			return nil
		}
		if s.job != nil && s.job.log.HandleKey(v) {
			return nil
		}
		if s.menu.HandleKey(v) {
			return nil
		}
		switch {
		case isBack(v):
			c.scr = scrHome
		case v.Ctrl && v.Rune == 'x' && s.job != nil && s.job.running:
			s.job.cancel()
		case v.Code == input.Enter:
			it, ok := s.menu.Selected()
			if !ok {
				return nil
			}
			switch it.Value.(string) {
			case "login":
				s.typing, s.msg, s.bad = true, "", false
			case "logout":
				if !c.d.Config.HasToken() {
					c.setFlash("no token to remove", true)
					return nil
				}
				c.confirm(&widgets.Dialog{Title: "Log out", Body: "Remove the saved token from " + config.CredentialsPath() + "?\n\nPublishing needs a token again afterwards.",
					Buttons: []string{"Log out", "Cancel"}, Choice: 1, Danger: true}, func(ch int) Cmd {
					if ch == 0 {
						msg, ok := c.logout()
						s.msg, s.bad = msg, !ok
					}
					return nil
				})
			case "check":
				return s.runCheck(c)
			case "update":
				return s.runUpdate(c)
			}
		}
	}
	return nil
}

func (s *settingsScreen) view(c *Console, w, h int) ([]string, []widgets.Binding) {
	s.ensure()
	t := c.th
	cfg := c.d.Config
	tok := t.R(t.Warn, "none") + t.R(t.Muted, " (tools are free; publishing needs one)")
	if cfg.HasToken() {
		tok = t.R(t.OK, masked(cfg)) + t.R(t.Muted, " from "+string(cfg.TokenSource))
	}
	out := []string{
		t.R(t.Title, "Account"),
		kv(t, "api base", cfg.BaseURL+t.R(t.Muted, "  ("+apiBaseSource(cfg.BaseURL)+")")),
		kv(t, "token", tok),
		kv(t, "key id", orDash(cfg.KeyID)),
		kv(t, "credentials", orDash(cfg.CredPath)),
	}
	if s.typing {
		out = append(out, "", "  "+t.R(t.Title, "Token")+"  "+s.token.View(t, max(10, w-12), true),
			t.R(t.Muted, "  It is saved to the credentials file, readable only by you, and never shown."))
	}
	if s.msg != "" {
		st := t.OK
		if s.bad {
			st = t.Bad
		}
		out = append(out, "  "+t.R(st, clip(s.msg, w-2)))
	}

	out = append(out, "", t.R(t.Title, "L80"), kv(t, "version", c.d.Version))
	switch {
	case s.checking:
		out = append(out, kv(t, "latest", t.R(t.Muted, widgets.Spin(c.tick)+" checking…")))
	case s.checkErr != "":
		out = append(out, kv(t, "latest", t.R(t.Bad, clip(style.Sanitize(s.checkErr), w-14))))
	case s.check != nil:
		latest := t.R(t.OK, s.check.Latest+" (up to date)")
		if s.check.UpdateAvailable {
			latest = t.R(t.Warn, s.check.Latest+" available")
		}
		out = append(out, kv(t, "latest", latest))
		for _, a := range s.check.Actions {
			for _, l := range style.Wrap(style.Sanitize(a), w-4) {
				out = append(out, t.R(t.Muted, "  "+l))
			}
		}
	}
	out = append(out, "")
	menuH := len(s.menu.Items) * 2
	out = append(out, s.menu.View(t, w, menuH, !s.typing)...)

	rest := h - len(out) - 1
	switch {
	case s.job != nil:
		out = append(out, "", jobHeader(c, s.job, w))
		out = append(out, s.job.log.View(t, w, max(3, rest-1))...)
	case s.check != nil && strings.TrimSpace(s.check.Notes) != "" && rest > 2:
		out = append(out, "", t.R(t.Title, "What's new in "+s.check.Latest))
		var notes []string
		for _, l := range strings.Split(style.Sanitize(s.check.Notes), "\n") {
			notes = append(notes, hang(strings.TrimRight(strings.Replace(l, "- ", "• ", 1), "\r"), w-2)...)
		}
		if len(notes) > rest-1 {
			notes = append(notes[:max(0, rest-2)], t.R(t.Muted, "…the rest is in CHANGELOG.md"))
		}
		for _, l := range notes {
			out = append(out, "  "+l)
		}
	}
	keys := []widgets.Binding{{Keys: "↑↓", Desc: "move"}, {Keys: "enter", Desc: "run"}, {Keys: "esc", Desc: "back"}}
	if s.typing {
		keys = []widgets.Binding{{Keys: "enter", Desc: "save"}, {Keys: "esc", Desc: "cancel"}}
	}
	for i := range out {
		out[i] = style.Truncate(out[i], w, "…")
	}
	return style.Fit(out, w, h), keys
}
