package main

// A fake GitHub releases page, for the update notice, gg upgrade and the
// install scripts. It answers the three routes they use:
//
//	HEAD /latest                  302 to /tag/<tag>
//	HEAD /tag/<tag>               200
//	GET  /download/<tag>/<file>   the file

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const defaultTimeoutForTests = 5 * time.Second

type fakeRelease struct {
	srv *httptest.Server
	tag string
	// latest is where /latest redirects; tag unless a test says otherwise.
	latest string
	hits   atomic.Int32
	// files is what /download/<tag>/ serves, by name. Tests edit it to tamper.
	files map[string][]byte
}

// serveRelease publishes tag with bin as this platform's gg binary, and a
// checksums.txt that matches it. It points GAGARIN_RELEASES at itself and
// moves the config directory, so the update-check cache is the test's own.
func serveRelease(t *testing.T, tag string, bin []byte) *fakeRelease {
	t.Helper()
	r := &fakeRelease{tag: tag, latest: tag, files: map[string][]byte{}}
	asset, archive := releaseArchive(t, tag, runtime.GOOS, runtime.GOARCH, bin)
	r.files[asset] = archive
	sum := sha256.Sum256(archive)
	r.files["checksums.txt"] = []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")

	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		switch {
		case req.URL.Path == "/latest":
			http.Redirect(w, req, "/tag/"+r.latest, http.StatusFound)
		case req.URL.Path == "/tag/"+r.latest:
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(req.URL.Path, "/download/"+r.tag+"/"):
			data, ok := r.files[strings.TrimPrefix(req.URL.Path, "/download/"+r.tag+"/")]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Write(data)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	t.Setenv("GAGARIN_RELEASES", r.srv.URL)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return r
}

// releaseArchive packs bin the way goreleaser does: gg (gg.exe on Windows)
// beside a README, in a tar.gz, or a zip for Windows.
func releaseArchive(t *testing.T, tag, goos, goarch string, bin []byte) (string, []byte) {
	t.Helper()
	name := fmt.Sprintf("gg_%s_%s_%s", strings.TrimPrefix(tag, "v"), goos, goarch)
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		for file, data := range map[string][]byte{"README.md": []byte("readme"), "gg.exe": bin} {
			f, err := zw.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			f.Write(data)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return name + ".zip", buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{"README.md", []byte("readme")}, {"gg", bin}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg})
		tw.Write(f.data)
	}
	tw.Close()
	gz.Close()
	return name + ".tar.gz", buf.Bytes()
}

// asVersion makes the running gg report v (e.g. "0.53.0", as goreleaser
// stamps it) for the length of the test.
func asVersion(t *testing.T, v string) {
	t.Helper()
	prev := version
	version = v
	t.Cleanup(func() { version = prev })
}

func TestLatestTagReadsTheRedirect(t *testing.T) {
	serveRelease(t, "v0.54.0", []byte("new"))
	tag, err := latestTag(defaultTimeoutForTests)
	if err != nil {
		t.Fatal(err)
	}
	if tag != "v0.54.0" {
		t.Fatalf("latest = %q, want v0.54.0", tag)
	}
}

func TestLatestTagRefusesSomethingThatIsNotAVersion(t *testing.T) {
	serveRelease(t, "nightly", []byte("new"))
	if tag, err := latestTag(defaultTimeoutForTests); err == nil {
		t.Fatalf("latest = %q; want an error for a tag that is not a version", tag)
	}
}

func TestCurrentTagAndNewer(t *testing.T) {
	asVersion(t, "0.53.0")
	if got := currentTag(); got != "v0.53.0" {
		t.Fatalf("currentTag = %q, want v0.53.0", got)
	}
	for _, c := range []struct {
		latest string
		want   bool
	}{{"v0.54.0", true}, {"v0.53.0", false}, {"v0.52.9", false}, {"v1.0.0", true}} {
		if got := newer(c.latest, currentTag()); got != c.want {
			t.Errorf("newer(%s, v0.53.0) = %v, want %v", c.latest, got, c.want)
		}
	}

	asVersion(t, "dev")
	if got := currentTag(); got != "" {
		t.Fatalf("a checkout build reports %q; want no version", got)
	}
	if newer("v9.0.0", currentTag()) {
		t.Fatal("a build with no version must never be told it is behind")
	}
}
