package api

import (
	"bytes"
	"strings"
	"testing"
)

func TestExitCodeForMapsServerCodes(t *testing.T) {
	cases := map[string]int{
		"E_SCHEMA_INVALID":     ExitValidation,
		"E_PAYLOAD_UNSAFE":     ExitValidation,
		"E_JSON_INVALID":       ExitValidation,
		"E_TOKEN_MISSING":      ExitAuth,
		"E_UNAUTHORIZED":       ExitAuth,
		"E_RATE_LIMITED":       ExitRateLimited,
		"E_QUOTA_EXCEEDED":     ExitRateLimited,
		"E_PAYLOAD_TOO_LARGE":  ExitPayloadTooBig,
		"E_TEMPLATE_UNKNOWN":   ExitUnknownTemplate,
		"E_TEMPLATE_MISSING":   ExitUnknownTemplate,
		"E_NETWORK":            ExitNetwork,
		"E_USAGE":              ExitUsage,
		"E_INPUT_INVALID":      ExitInputInvalid,
		"E_UPDATE_FAILED":      ExitInternal,
		"E_INPUT_NOT_TEMPLATE": ExitInputInvalid,
		"E_SERVER":             ExitServer,
		"E_TOOL_UNAVAILABLE":   ExitToolUnavailable,
	}
	for code, want := range cases {
		if got := ExitCodeFor(code); got != want {
			t.Errorf("ExitCodeFor(%s) = %d, want %d", code, got, want)
		}
	}
}

// A code the CLI has never heard of must still exit non-zero rather than
// pretending success, so the server can add codes without a CLI release.
func TestExitCodeForUnknownCodeIsNonZero(t *testing.T) {
	if got := ExitCodeFor("E_SOMETHING_NEW"); got != ExitInternal {
		t.Errorf("unknown code = %d, want %d", got, ExitInternal)
	}
	if ExitCodeFor("E_SOMETHING_NEW") == ExitOK {
		t.Error("an unknown error code must never map to exit 0")
	}
}

func TestPrintIncludesCodeDetailsAndRemedy(t *testing.T) {
	e := &Error{
		Code:    "E_SCHEMA_INVALID",
		Message: "Payload does not match the schema.",
		Remedy:  "Fix the fields and republish.",
		Details: []Detail{{Path: "/sections/0", Message: "must NOT have additional property: 'imageUrl'"}},
	}

	var buf bytes.Buffer
	e.Print(&buf)
	out := buf.String()

	for _, want := range []string{
		"error: E_SCHEMA_INVALID",
		"/sections/0",
		"imageUrl",
		"remedy: Fix the fields and republish.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
