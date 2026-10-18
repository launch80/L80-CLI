package pytool

import (
	"regexp"
	"strconv"
	"strings"
)

var versionRE = regexp.MustCompile(`(?:^|[^0-9.])v?(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion finds the first x.y[.z] in a tool's version output
// ("BetterBench 0.6.1", "pwdet, version 0.1.0") and returns it normalised to
// three components. ok is false when there is none.
func ParseVersion(out string) (string, bool) {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return "", false
	}
	patch := m[3]
	if patch == "" {
		patch = "0"
	}
	return m[1] + "." + m[2] + "." + patch, true
}

// Compare orders two dotted numeric versions: -1, 0 or 1. Missing components
// count as zero, so "0.6" == "0.6.0". Non-numeric input compares as zero.
func Compare(a, b string) int {
	pa, pb := split(a), split(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func split(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, f := range strings.Split(v, ".") {
		n, err := strconv.Atoi(f)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}
