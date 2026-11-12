package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/launch80/L80-CLI/internal/config"
)

// listingAPI fakes publish (recording ?listed and the body) and the visibility endpoint.
type listingAPI struct {
	srv        *httptest.Server
	listedSeen []string
	authorSeen []string // the ?author= of each publish ("" when absent)
	bodies     [][]byte
	visibility []string // "<guid> <listed>"
}

func newListingAPI(t *testing.T, template string, honoursListed bool) *listingAPI {
	t.Helper()
	a := &listingAPI{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/artifacts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		a.bodies = append(a.bodies, body)
		q := r.URL.Query().Get("listed")
		a.listedSeen = append(a.listedSeen, q)
		listed := honoursListed && q == "true"
		au := r.URL.Query().Get("author")
		a.authorSeen = append(a.authorSeen, au)
		author := `"k1"`
		if au == "false" {
			author = "null"
		}
		fmt.Fprintf(w, `{"ok":true,"data":{"guid":"11111111-2222-4333-8444-555555555555","url":"https://l80.test/a/11111111-2222-4333-8444-555555555555","template_id":%q,"template_version":1,"title":"Qwen3-8B","trust_tier":"unverified","listed":%v,"author":%s,"byte_size":4200,"created_at":"2026-11-12T01:02:03.000Z","quota":{"limit":10,"remaining":7,"window_hours":24}}}`, template, listed, author)
	})
	mux.HandleFunc("/api/v1/artifacts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasSuffix(r.URL.Path, "/visibility") {
			http.NotFound(w, r)
			return
		}
		guid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/artifacts/"), "/visibility")
		var in struct {
			Listed bool `json:"listed"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		a.visibility = append(a.visibility, fmt.Sprintf("%s %v", guid, in.Listed))
		fmt.Fprintf(w, `{"ok":true,"data":{"guid":%q,"url":"https://l80.test/a/%s","listed":%v}}`, guid, guid, in.Listed)
	})
	a.srv = httptest.NewServer(mux)
	t.Cleanup(a.srv.Close)
	return a
}

func listingEnv(t *testing.T) {
	t.Helper()
	isolateSettings(t)
	t.Setenv("L80_TOKEN", "L80_k1_secret")
	t.Setenv("L80_PUBLISH_PUBLIC", "")
	t.Setenv("L80_PUBLISH_AUTHOR", "")
}

// withTerminal makes stdin and stderr terminals and feeds answer to prompts.
func withTerminal(t *testing.T, answer string) {
	t.Helper()
	oldIn, oldErr, oldPrompt := stdinIsTTY, stderrIsTTY, promptIn
	stdinIsTTY = func() bool { return true }
	stderrIsTTY = func() bool { return true }
	promptIn = strings.NewReader(answer)
	t.Cleanup(func() { stdinIsTTY, stderrIsTTY, promptIn = oldIn, oldErr, oldPrompt })
}

func withoutTerminal(t *testing.T) {
	t.Helper()
	oldIn, oldErr := stdinIsTTY, stderrIsTTY
	stdinIsTTY = func() bool { return false }
	stderrIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY, stderrIsTTY = oldIn, oldErr })
}

func reportFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "report.json")
	body := `{"$template":"bench.report.v1","title":"t","summary":"s","system":{"model":"m"}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func genericFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "generic.json")
	if err := os.WriteFile(p, []byte(`{"$template":"generic.report.v1","title":"t","summary":"s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishPublicSendsListedAndSaysWhere(t *testing.T) {
	listingEnv(t)
	withoutTerminal(t)
	a := newListingAPI(t, "bench.report.v1", true)
	code, out, errOut := runCmd(t, runPublish, reportFile(t), "--public", "--api-base", a.srv.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if a.listedSeen[0] != "true" {
		t.Errorf("?listed = %q", a.listedSeen[0])
	}
	for _, want := range []string{
		`page         "Qwen3-8B" · bench.report.v1 v1 · 4.1 KB`,
		"who sees it  anyone: listed in the launch80 Directory",
		a.srv.URL + "/directory",
		"trust        self-reported until Launch80 verifies it",
		"quota        7 of 10 publishes left in the next 24 hours",
		"next         take it out of the Directory   L80 visibility https://l80.test/a/11111111-2222-4333-8444-555555555555 --private",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "verify request") {
		t.Error("bench.report.v1 cannot be verified; it must not be suggested")
	}
}

func TestPublishListsByDefault(t *testing.T) {
	listingEnv(t)
	withTerminal(t, "")
	a := newListingAPI(t, "bench.report.v1", true)
	code, out, errOut := runCmd(t, runPublish, reportFile(t), "--api-base", a.srv.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if a.listedSeen[0] != "true" || strings.Contains(errOut, "List this page") {
		t.Errorf("default: ?listed = %q, asked: %s", a.listedSeen[0], errOut)
	}
	if !strings.Contains(out, "anyone: listed in the launch80 Directory") {
		t.Errorf("out = %s", out)
	}
}

func TestPrivateSaysWhoSeesItAndHowToList(t *testing.T) {
	listingEnv(t)
	withoutTerminal(t)
	a := newListingAPI(t, "bench.report.v1", true)
	code, out, errOut := runCmd(t, runPublish, reportFile(t), "--private", "--api-base", a.srv.URL)
	if code != 0 || a.listedSeen[0] != "" {
		t.Fatalf("exit %d, ?listed %q: %s", code, a.listedSeen[0], errOut)
	}
	for _, want := range []string{
		"who sees it  only people you send the link to (unlisted)",
		"next         list it in the Directory       L80 visibility https://l80.test/a/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestPublishAsksOnTerminalWhenSetToAsk(t *testing.T) {
	for answer, want := range map[string]string{"y\n": "true", "yes\n": "true", "n\n": "", "\n": "", "": ""} {
		listingEnv(t)
		t.Setenv("L80_PUBLISH_PUBLIC", "ask")
		withTerminal(t, answer)
		a := newListingAPI(t, "bench.report.v1", true)
		code, _, errOut := runCmd(t, runPublish, reportFile(t), "--api-base", a.srv.URL)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if a.listedSeen[0] != want {
			t.Errorf("answer %q: ?listed = %q, want %q", answer, a.listedSeen[0], want)
		}
		if !strings.Contains(errOut, "List this page in the public launch80 Directory") {
			t.Errorf("answer %q: no question asked: %s", answer, errOut)
		}
	}
}

func TestPublishJSONNeverAsks(t *testing.T) {
	listingEnv(t)
	t.Setenv("L80_PUBLISH_PUBLIC", "ask")
	withTerminal(t, "y\n")
	a := newListingAPI(t, "bench.report.v1", true)
	code, out, errOut := runCmd(t, runPublish, reportFile(t), "--json", "--api-base", a.srv.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if a.listedSeen[0] != "" || strings.Contains(errOut, "List this page") {
		t.Errorf("--json asked or listed: %q %s", a.listedSeen[0], errOut)
	}
	if !strings.Contains(out, `"listed": false`) {
		t.Errorf("json = %s", out)
	}
}

func TestPublishSettingDecides(t *testing.T) {
	for mode, want := range map[string]string{"always": "true", "never": ""} {
		listingEnv(t)
		withTerminal(t, "y\n")
		if err := config.Set(config.KeyPublishPublic, mode); err != nil {
			t.Fatal(err)
		}
		a := newListingAPI(t, "bench.report.v1", true)
		code, _, errOut := runCmd(t, runPublish, reportFile(t), "--api-base", a.srv.URL)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		if a.listedSeen[0] != want || strings.Contains(errOut, "List this page") {
			t.Errorf("%s: ?listed = %q, asked: %v", mode, a.listedSeen[0], strings.Contains(errOut, "List this page"))
		}
	}
	// ask with no terminal to ask on: unlisted, and the output says why.
	listingEnv(t)
	withoutTerminal(t)
	_ = config.Set(config.KeyPublishPublic, "ask")
	a0 := newListingAPI(t, "bench.report.v1", true)
	if code, out, errOut := runCmd(t, runPublish, reportFile(t), "--api-base", a0.srv.URL); code != 0 || a0.listedSeen[0] != "" || !strings.Contains(out, "no terminal to ask on") {
		t.Errorf("ask without a terminal: exit %d, ?listed %q\n%s%s", code, a0.listedSeen[0], out, errOut)
	}
	// --private beats publish.public=always.
	listingEnv(t)
	withoutTerminal(t)
	_ = config.Set(config.KeyPublishPublic, "always")
	a := newListingAPI(t, "bench.report.v1", true)
	if code, _, errOut := runCmd(t, runPublish, reportFile(t), "--private", "--api-base", a.srv.URL); code != 0 || a.listedSeen[0] != "" {
		t.Errorf("--private: exit %d, ?listed %q: %s", code, a.listedSeen[0], errOut)
	}
}

func TestPublishListingUsageErrors(t *testing.T) {
	listingEnv(t)
	withoutTerminal(t)
	a := newListingAPI(t, "generic.report.v1", true)
	code, _, errOut := runCmd(t, runPublish, reportFile(t), "--public", "--private", "--api-base", a.srv.URL)
	if code != 2 || !strings.Contains(errOut, "--public and --private were both given") {
		t.Errorf("both: exit %d: %s", code, errOut)
	}
	code, _, errOut = runCmd(t, runPublish, genericFile(t), "--public", "--api-base", a.srv.URL)
	if code != 2 || !strings.Contains(errOut, "generic.report.v1 pages cannot be listed") {
		t.Errorf("generic --public: exit %d: %s", code, errOut)
	}
	if len(a.bodies) != 0 {
		t.Error("a refused publish reached the server")
	}
	// A generic page publishes without a question or a hint.
	withTerminal(t, "y\n")
	code, out, errOut := runCmd(t, runPublish, genericFile(t), "--api-base", a.srv.URL)
	if code != 0 || strings.Contains(errOut, "List this page") || strings.Contains(out, "unlisted") || a.listedSeen[0] != "" {
		t.Errorf("generic: exit %d\nout %s\nerr %s", code, out, errOut)
	}
}

func TestPublishWarnsWhenServerIgnoresListed(t *testing.T) {
	listingEnv(t)
	withoutTerminal(t)
	a := newListingAPI(t, "bench.report.v1", false)
	code, _, errOut := runCmd(t, runPublish, reportFile(t), "--public", "--api-base", a.srv.URL)
	if code != 0 || !strings.Contains(errOut, "the server did not list the page") {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestVisibility(t *testing.T) {
	listingEnv(t)
	a := newListingAPI(t, "bench.report.v1", true)
	guid := "abcdef12-3456-4789-8abc-def012345678"
	code, out, errOut := runCmd(t, runVisibility, "https://l80.test/a/"+guid, "--public", "--api-base", a.srv.URL)
	if code != 0 || !strings.Contains(out, "listed: https://l80.test/a/"+guid) {
		t.Fatalf("exit %d\nout %s\nerr %s", code, out, errOut)
	}
	code, out, _ = runCmd(t, runVisibility, guid, "--private", "--api-base", a.srv.URL, "--json")
	if code != 0 || !strings.Contains(out, `"listed": false`) {
		t.Fatalf("exit %d: %s", code, out)
	}
	if strings.Join(a.visibility, ",") != guid+" true,"+guid+" false" {
		t.Errorf("server saw %v", a.visibility)
	}
	for _, args := range [][]string{{guid}, {guid, "--public", "--private"}, {"--public"}} {
		if code, _, _ := runCmd(t, runVisibility, append(args, "--api-base", a.srv.URL)...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestBetterbenchGPUFlagAndPrompt(t *testing.T) {
	listingEnv(t)
	withoutTerminal(t)
	a := newListingAPI(t, "bench.betterbench.v1", true)
	results := filepath.Join("..", "betterbench", "testdata", "results_schema2_v061.json")
	if _, err := os.Stat(results); err != nil {
		t.Skip("no BetterBench fixture")
	}
	code, _, errOut := runCmd(t, runBetterbench, "--results", results, "--gpu", "RTX 4090", "--private", "--api-base", a.srv.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(string(a.bodies[0]), `"gpu":"RTX 4090"`) {
		t.Errorf("--gpu not in the body")
	}
	// Listed on a terminal with no GPU named (a remote run): L80 asks for one.
	var doc map[string]any
	raw, _ := os.ReadFile(results)
	_ = json.Unmarshal(raw, &doc)
	doc["env"].(map[string]any)["gpu"] = map[string]any{}
	remote := filepath.Join(t.TempDir(), "remote.json")
	raw, _ = json.Marshal(doc)
	_ = os.WriteFile(remote, raw, 0o644)
	withTerminal(t, "Arc Pro B65 32GB\n") // listed by default, so the only question is the GPU
	code, _, errOut = runCmd(t, runBetterbench, "--results", remote, "--api-base", a.srv.URL)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "Which GPU ran this?") || !strings.Contains(string(a.bodies[1]), `"gpu":"Arc Pro B65 32GB"`) || a.listedSeen[1] != "true" {
		t.Errorf("no GPU prompt or not applied\n%s", errOut)
	}
	// A run whose GPU BetterBench detected is never asked about.
	withTerminal(t, "y\n")
	_, _, errOut = runCmd(t, runBetterbench, "--results", results, "--api-base", a.srv.URL)
	if strings.Contains(errOut, "Which GPU ran this?") {
		t.Error("asked for a GPU the run already names")
	}
}

func TestVastRunPassesListingDown(t *testing.T) {
	for _, tc := range []struct {
		flag, want string
	}{{"--public", "always"}, {"", "always"}, {"--private", "never"}} {
		listingEnv(t)
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		t.Setenv("BBVASTL80_RUNS_DIR", "")
		fakeTool(t, "L80_VAST", `echo "listing:$L80_PUBLISH_PUBLIC"; exit 0`+"\n")
		args := []string{"run", "--spec", "x.yaml"}
		if tc.flag != "" {
			args = append(args, tc.flag)
		}
		_, out, errOut := runCmd(t, runVast, args...)
		if !strings.Contains(out+errOut, "listing:"+tc.want) {
			t.Errorf("%q: want listing:%s in\n%s%s", tc.flag, tc.want, out, errOut)
		}
	}
}

func TestPublishShowsTheAuthorUnlessNoAuthor(t *testing.T) {
	listingEnv(t)
	t.Setenv("L80_PUBLISH_AUTHOR", "")
	withoutTerminal(t)
	a := newListingAPI(t, "generic.report.v1", true)
	code, out, errOut := runCmd(t, runPublish, genericFile(t), "--api-base", a.srv.URL)
	if code != 0 || a.authorSeen[0] != "" || !strings.Contains(out, "author       k1 (shown on the page") {
		t.Fatalf("default: exit %d, ?author=%q\n%s%s", code, a.authorSeen[0], out, errOut)
	}
	code, out, errOut = runCmd(t, runPublish, genericFile(t), "--no-author", "--api-base", a.srv.URL)
	if code != 0 || a.authorSeen[1] != "false" || !strings.Contains(out, "author       not shown (--no-author)") {
		t.Fatalf("--no-author: exit %d, ?author=%q\n%s%s", code, a.authorSeen[1], out, errOut)
	}
	// publish.author hide does the same for every publish.
	t.Setenv("L80_PUBLISH_AUTHOR", "hide")
	if code, _, errOut := runCmd(t, runPublish, genericFile(t), "--api-base", a.srv.URL); code != 0 || a.authorSeen[2] != "false" {
		t.Fatalf("hide: exit %d, ?author=%q: %s", code, a.authorSeen[2], errOut)
	}
}

func TestVastRunPassesNoAuthorDown(t *testing.T) {
	listingEnv(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("BBVASTL80_RUNS_DIR", "")
	fakeTool(t, "L80_VAST", `echo "author:$L80_PUBLISH_AUTHOR"; exit 0`+"\n")
	_, out, errOut := runCmd(t, runVast, "run", "--spec", "x.yaml", "--no-author")
	if !strings.Contains(out+errOut, "author:hide") {
		t.Errorf("want author:hide in\n%s%s", out, errOut)
	}
}
