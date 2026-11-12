package betterbench

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// MaxGPUNote is the longest GPU name a note can carry (the server's limit on a
// note value is 200 characters; a GPU name never needs that).
const MaxGPUNote = 120

// NamesGPU reports whether a results file (or a body prepared from one) says
// which GPU ran it: BetterBench's own detection, which only works when it runs
// on the serving machine, or a gpu= / hardware= note. The launch80 directory
// groups runs by this; a run that names none is filed under "Not stated".
func NamesGPU(data []byte) bool {
	var top struct {
		Env struct {
			GPU   GPU               `json:"gpu"`
			Notes map[string]string `json:"notes"`
		} `json:"env"`
	}
	if json.Unmarshal(data, &top) != nil {
		return false
	}
	g := top.Env.GPU
	if strings.TrimSpace(g.Vendor) != "" || nonEmpty(g.NvidiaSMI) || nonEmpty(g.ROCmSMIProductName) {
		return true
	}
	for _, k := range []string{"gpu", "hardware"} {
		if strings.TrimSpace(top.Env.Notes[k]) != "" {
			return true
		}
	}
	return false
}

func nonEmpty(xs []string) bool {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return true
		}
	}
	return false
}

// CleanGPU trims a GPU name and checks its length.
func CleanGPU(name string) (string, error) {
	s := strings.Join(strings.Fields(name), " ")
	if len(s) > MaxGPUNote {
		return "", &InputError{fmt.Sprintf("--gpu is %d characters; at most %d", len(s), MaxGPUNote)}
	}
	return s, nil
}

// WithGPUNote sets env.notes.gpu in a prepared body, keeping every other value
// as the bytes it was. An empty name leaves the body unchanged.
func WithGPUNote(body []byte, gpu string) ([]byte, error) {
	gpu, err := CleanGPU(gpu)
	if err != nil || gpu == "" {
		return body, err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSyntax, err)
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(top["env"], &env); err != nil {
		return nil, fmt.Errorf("%w: env is not an object", ErrNotBetterBench)
	}
	notes := map[string]string{}
	if raw, ok := env["notes"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &notes); err != nil {
			return nil, &InputError{"env.notes is not an object of strings"}
		}
	}
	notes["gpu"] = gpu
	if env["notes"], err = json.Marshal(notes); err != nil {
		return nil, err
	}
	if top["env"], err = json.Marshal(env); err != nil {
		return nil, err
	}
	out, err := json.Marshal(top)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
