// Package api talks to the launch80 artifacts API.
package api

import (
	"fmt"
	"io"
)

// Exit codes. A model reads these to decide what to do next, so each maps to a
// distinct remedy rather than a generic failure.
const (
	ExitOK              = 0
	ExitInternal        = 1
	ExitUsage           = 2
	ExitInputInvalid    = 3
	ExitValidation      = 4
	ExitAuth            = 5
	ExitRateLimited     = 6
	ExitNetwork         = 7
	ExitServer          = 8
	ExitPayloadTooBig   = 9
	ExitUnknownTemplate = 10
	// ExitToolUnavailable means a tool L80 fronts (BetterBench, bbvastl80,
	// pwdet, the dashboard) cannot be run here. A wrapped tool's own non-zero
	// exit status is passed through unchanged and never maps to this.
	ExitToolUnavailable = 11
)

// Detail is one field-level validation problem.
type Detail struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Error is the server's structured error, or a locally generated one in the
// same shape so callers only ever handle one type.
type Error struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	Remedy    string   `json:"remedy"`
	Details   []Detail `json:"details,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Newf builds a local error in the server's shape.
func Newf(code, remedy, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Remedy: remedy}
}

// ExitCodeFor maps a server error code to a process exit code.
//
// Unknown codes deliberately fall through to ExitInternal while the message and
// remedy are still printed verbatim, so the server can introduce a new code
// without requiring a CLI release to stay useful.
func ExitCodeFor(code string) int {
	switch code {
	case "E_SCHEMA_INVALID", "E_PAYLOAD_UNSAFE", "E_JSON_INVALID":
		return ExitValidation
	case "E_TOKEN_MISSING", "E_UNAUTHORIZED":
		return ExitAuth
	case "E_RATE_LIMITED", "E_QUOTA_EXCEEDED":
		return ExitRateLimited
	case "E_PAYLOAD_TOO_LARGE":
		return ExitPayloadTooBig
	case "E_TEMPLATE_UNKNOWN", "E_TEMPLATE_MISSING":
		return ExitUnknownTemplate
	// A conflict is the caller contradicting themselves, not the server
	// rejecting an id — the fix is deciding which one they meant.
	case "E_TEMPLATE_CONFLICT":
		return ExitUsage
	case "E_NETWORK":
		return ExitNetwork
	case "E_INPUT_INVALID", "E_INPUT_NOT_TEMPLATE":
		return ExitInputInvalid
	case "E_USAGE":
		return ExitUsage
	case "E_NOT_FOUND", "E_INTERNAL", "E_SERVER":
		return ExitServer
	case "E_TOOL_UNAVAILABLE":
		return ExitToolUnavailable
	case "E_VERIFY_EXISTS", "E_VERIFY_INELIGIBLE":
		return ExitValidation
	case "E_FORBIDDEN_ROLE":
		return ExitAuth
	case "E_NOT_SUPPORTED":
		return ExitServer
	case "E_UPDATE_FAILED":
		return ExitInternal
	default:
		return ExitInternal
	}
}

// Print writes the error in the prescriptive form the skill tells the model to
// read: the code, what happened, each offending field, then the remedy.
func (e *Error) Print(w io.Writer) {
	fmt.Fprintf(w, "error: %s\n", e.Code)
	fmt.Fprintf(w, "  %s\n", e.Message)
	for _, d := range e.Details {
		fmt.Fprintf(w, "  %-24s %s\n", d.Path, d.Message)
	}
	if e.Remedy != "" {
		fmt.Fprintf(w, "remedy: %s\n", e.Remedy)
	}
	if e.RequestID != "" {
		fmt.Fprintf(w, "request_id: %s\n", e.RequestID)
	}
}
