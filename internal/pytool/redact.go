package pytool

import "strings"

// sensitiveFlags name values that must never be echoed. Matched on the flag
// name after stripping leading dashes, so --api-key, -api-key and --api-key=x
// are all covered.
var sensitiveFlags = map[string]bool{
	"api-key": true, "api-key-a": true, "api-key-b": true, "token": true, "hf-token": true,
	"password": true, "secret": true, "vast-api-key": true, "key": true,
}

// sensitiveEnvParts flag a KEY=value echo (as in `env FOO=bar cmd`) as secret.
var sensitiveEnvParts = []string{"TOKEN", "KEY", "SECRET", "PASSWORD"}

const redacted = "***"

// RedactArgv returns a copy of argv safe to print: the value after a sensitive
// flag, the value inside --flag=value, and the value of a KEY=value word whose
// key looks secret are replaced with ***. The command itself is never altered.
func RedactArgv(argv []string) []string {
	out := make([]string, len(argv))
	hide := false
	for i, a := range argv {
		switch {
		case hide:
			out[i] = redacted
			hide = false
		case strings.HasPrefix(a, "-"):
			name, _, hasEq := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if sensitiveFlags[strings.ToLower(name)] {
				if hasEq {
					out[i] = a[:strings.Index(a, "=")+1] + redacted
				} else {
					out[i] = a
					hide = true
				}
			} else {
				out[i] = a
			}
		case isSecretAssignment(a):
			k, _, _ := strings.Cut(a, "=")
			out[i] = k + "=" + redacted
		default:
			out[i] = a
		}
	}
	return out
}

func isSecretAssignment(word string) bool {
	k, _, ok := strings.Cut(word, "=")
	if !ok || k == "" || strings.ContainsAny(k, " /") {
		return false
	}
	upper := strings.ToUpper(k)
	for _, p := range sensitiveEnvParts {
		if strings.Contains(upper, p) {
			return true
		}
	}
	return false
}

// FormatArgv renders argv as one copy-pasteable shell line, redacted, with
// any word that needs it single-quoted.
func FormatArgv(argv []string) string {
	words := RedactArgv(argv)
	for i, w := range words {
		if w == "" || strings.ContainsAny(w, " \t\n'\"\\$`*?[]{}()<>|&;#~!") {
			words[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		}
	}
	return strings.Join(words, " ")
}
