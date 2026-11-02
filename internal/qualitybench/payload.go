package qualitybench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// TemplateID is the server template for a QualityBench results file.
const TemplateID = "bench.qualitybench.v1"

// MaxPayloadBytes mirrors the server's limit for TemplateID.
const MaxPayloadBytes = 256 * 1024

// SchemaVersion is the results schema this L80 publishes.
const SchemaVersion = 1

var (
	ErrSyntax          = errors.New("not valid JSON")
	ErrNotQualityBench = errors.New("not a QualityBench results file")
	ErrAlreadyPayload  = errors.New("a payload for another template")
)

// LeakError names a value that would publish something about this machine.
type LeakError struct{ Path, Why string }

func (e *LeakError) Error() string { return fmt.Sprintf("%s %s", e.Path, e.Why) }

// strippedEnvKeys never leave the machine, as for BetterBench.
var strippedEnvKeys = []string{"endpoint", "host"}

// Prepare turns a results file into the body to publish: it checks the
// schema version, removes env.endpoint and env.host, and sets $template.
// Values are copied as raw bytes, so no number is re-encoded. A string that
// would still reveal the machine (a home-directory path, a LAN URL) is an
// error rather than something to scrub silently: the tool should not write it.
func Prepare(data []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSyntax, err)
	}
	if raw, ok := top["$template"]; ok {
		var s string
		if json.Unmarshal(raw, &s) != nil || s != TemplateID {
			return nil, ErrAlreadyPayload
		}
	}
	var ver int
	if raw, ok := top["schema_version"]; !ok || json.Unmarshal(raw, &ver) != nil {
		return nil, fmt.Errorf("%w: no schema_version", ErrNotQualityBench)
	}
	if ver != SchemaVersion {
		return nil, fmt.Errorf("%w: schema_version %d (this L80 publishes %d; run `L80 update`)", ErrNotQualityBench, ver, SchemaVersion)
	}
	if _, ok := top["model"]; !ok {
		return nil, fmt.Errorf("%w: model is missing", ErrNotQualityBench)
	}
	if raw, ok := top["env"]; ok {
		var env map[string]json.RawMessage
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("%w: env is not an object", ErrNotQualityBench)
		}
		for _, k := range strippedEnvKeys {
			delete(env, k)
		}
		b, err := json.Marshal(env)
		if err != nil {
			return nil, err
		}
		top["env"] = b
	}
	top["$template"] = json.RawMessage(`"` + TemplateID + `"`)

	out, err := json.Marshal(top)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, out); err != nil {
		return nil, err
	}
	var tree any
	if err := json.Unmarshal(buf.Bytes(), &tree); err != nil {
		return nil, err
	}
	if err := checkLeaks("", tree); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	homePath = regexp.MustCompile(`(^|[\s"'(=])(/home/|/Users/|/root/|~/|[A-Za-z]:\\Users\\)`)
	lanURL   = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.|\[?::1|[^/\s]*\.local\b)`)
)

func checkLeaks(path string, v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if err := checkLeaks(path+"/"+k, c); err != nil {
				return err
			}
		}
	case []any:
		for i, c := range x {
			if err := checkLeaks(fmt.Sprintf("%s/%d", path, i), c); err != nil {
				return err
			}
		}
	case string:
		switch {
		case lanURL.MatchString(x):
			return &LeakError{path, "contains a local or LAN URL"}
		case homePath.MatchString(x):
			return &LeakError{path, "contains a home-directory path"}
		}
	}
	return nil
}

// StripsIdentity reports whether a prepared body is free of env.endpoint and
// env.host. Publish checks it once more before sending.
func StripsIdentity(body []byte) bool {
	var top struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return false
	}
	for _, k := range strippedEnvKeys {
		if _, ok := top.Env[k]; ok {
			return false
		}
	}
	return true
}

// Headline is a one-line summary of a results file for the CLI and the TUI.
func Headline(data []byte) []string {
	var r struct {
		Model string `json:"model"`
		Tasks []struct {
			Label string `json:"label"`
			Off   *struct {
				Acc float64 `json:"acc"`
				K   int     `json:"k"`
				N   int     `json:"n"`
			} `json:"off"`
			On *struct {
				Acc float64 `json:"acc"`
			} `json:"on"`
		} `json:"tasks"`
		Partials []string `json:"partials"`
	}
	if json.Unmarshal(data, &r) != nil {
		return nil
	}
	var out []string
	for _, t := range r.Tasks {
		if t.Off == nil {
			continue
		}
		l := fmt.Sprintf("%s: %.1f%% (%d/%d)", t.Label, t.Off.Acc, t.Off.K, t.Off.N)
		if t.On != nil {
			l += fmt.Sprintf(", thinking on %.1f%%", t.On.Acc)
		}
		out = append(out, l)
	}
	if len(r.Partials) > 0 {
		out = append(out, "partial: "+strings.Join(r.Partials, ", "))
	}
	return out
}
