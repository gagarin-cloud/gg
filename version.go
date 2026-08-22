package main

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// version is stamped by the release pipeline via -ldflags "-X main.version=...".
// A plain `go build` in a checkout leaves it "dev", which is the honest answer
// for a binary that came from working-tree source.
var version = "dev"

// versionString prefers the stamped value, then what the module system knows.
// `go install github.com/gagarin-cloud/gg@v0.1.0` sets no ldflags, but the build info
// records the version it resolved — so reporting "dev" there would be wrong, and
// "which gg am I running" is the first question of any bug report.
func versionString() string {
	v := version
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	return fmt.Sprintf("gg %s (%s/%s, %s)", v, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

func cmdVersion() error {
	fmt.Println(versionString())
	return nil
}
