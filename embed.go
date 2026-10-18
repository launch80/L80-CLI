// Package l80skills holds the canonical skill sources.
//
// The embed lives at the module root because //go:embed cannot reach outside
// its own package directory, and we want skills/ to stay a browsable top-level
// directory rather than being buried under internal/.
package l80skills

import "embed"

//go:embed all:skills
var FS embed.FS

// SkillNames lists the skills this binary ships.
var SkillNames = []string{"l80", "l80-test-report", "l80-vast", "l80-pwdet"}
