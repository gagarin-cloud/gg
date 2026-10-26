package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"time"
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

// cmdVersion also says when a newer gg exists, on stderr so stdout stays the one
// line scripts read. Unlike the notice it does not wait for a terminal: an agent
// checking `gg version` is exactly who should hear that it is behind.
func cmdVersion() error {
	fmt.Println(versionString())
	if os.Getenv("GAGARIN_NO_UPDATE_CHECK") != "" || currentTag() == "" {
		return nil
	}
	tag, err := latestTag(2 * time.Second)
	if err != nil {
		return nil
	}
	writeUpdateCheck(updateCheck{CheckedAt: time.Now(), Latest: tag})
	printUpdateNotice(os.Stderr, tag)
	return nil
}
