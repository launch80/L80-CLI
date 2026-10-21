package cli

import (
	"fmt"
	"runtime"
)

// Version is overridden at build time via -ldflags.
var Version = "1.2.0"

func VersionLine() string {
	return fmt.Sprintf("L80 %s (%s %s/%s)", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
