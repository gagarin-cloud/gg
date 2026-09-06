package main

// The tunnel's local half: which ports are read off the connection variables,
// how those variables are rewritten to point at the tunnel, and that a refusal
// on the websocket handshake still arrives as the API's `[code] message`.

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTunnelPortsOf(t *testing.T) {
	// A postgres publishes one port; a qdrant two, and the primary — the one
	// <NAME>_URL addresses — must come first whatever the map order was.
	tuns := tunnelPortsOf("VECTORS", map[string]string{
		"VECTORS_URL":       "http://vectors:6333",
		"VECTORS_HOST":      "vectors",
		"VECTORS_PORT":      "6333",
		"VECTORS_GRPC_PORT": "6334",
		"VECTORS_API_KEY":   "k",
	})
	if len(tuns) != 2 {
		t.Fatalf("want 2 ports, got %d", len(tuns))
	}
	if !tuns[0].primary || tuns[0].remote != 6333 {
		t.Errorf("primary first: got %+v", tuns[0])
	}
	if tuns[1].key != "VECTORS_GRPC_PORT" || tuns[1].remote != 6334 {
		t.Errorf("extra port: got %+v", tuns[1])
	}

	// A value that is not a number is not a port, whatever its key says.
	if got := tunnelPortsOf("X", map[string]string{"X_PORT": "psql"}); len(got) != 0 {
		t.Errorf("non-numeric port accepted: %+v", got)
	}
}

func TestLocalizeEnv(t *testing.T) {
	// The qdrant shape exercises everything at once: a URL rewritten by the
	// port in its host, a second port with its own local end, a host, and a
	// credential that must pass through untouched.
	tuns := []*tunnelPort{
		{key: "VECTORS_PORT", remote: 6333, local: 16333, primary: true},
		{key: "VECTORS_GRPC_PORT", remote: 6334, local: 16334},
	}
	got := localizeEnv(map[string]string{
		"VECTORS_URL":       "http://vectors:6333",
		"VECTORS_HOST":      "vectors",
		"VECTORS_PORT":      "6333",
		"VECTORS_GRPC_PORT": "6334",
		"VECTORS_API_KEY":   "secret-key",
	}, "VECTORS", tuns)

	want := map[string]string{
		"VECTORS_URL":       "http://127.0.0.1:16333",
		"VECTORS_HOST":      "127.0.0.1",
		"VECTORS_PORT":      "16333",
		"VECTORS_GRPC_PORT": "16334",
		"VECTORS_API_KEY":   "secret-key",
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
}

func TestLocalizeEnvKeepsCredentialsInTheURL(t *testing.T) {
	// The URL is the value people paste, so the password must survive the
	// rewrite — for postgres in the userinfo, for valkey in the passwordless
	// user form redis clients expect.
	tuns := []*tunnelPort{{key: "DB_PORT", remote: 5432, local: 15432, primary: true}}
	got := localizeEnv(map[string]string{
		"DB_URL": "postgres://app:s3cr%40t@db:5432/app?sslmode=disable",
	}, "DB", tuns)
	if want := "postgres://app:s3cr%40t@127.0.0.1:15432/app?sslmode=disable"; got["DB_URL"] != want {
		t.Errorf("DB_URL = %q, want %q", got["DB_URL"], want)
	}

	tuns = []*tunnelPort{{key: "CACHE_PORT", remote: 6379, local: 16379, primary: true}}
	got = localizeEnv(map[string]string{
		"CACHE_URL": "redis://:pass@cache:6379",
	}, "CACHE", tuns)
	if want := "redis://:pass@127.0.0.1:16379"; got["CACHE_URL"] != want {
		t.Errorf("CACHE_URL = %q, want %q", got["CACHE_URL"], want)
	}
}

func TestLocalizeURLLeavesTheUnknownAlone(t *testing.T) {
	byRemote := map[string]int{"5432": 15432}
	for _, raw := range []string{
		"postgres://app:p@db:9999/app", // a port nothing tunnels
		"not a url at all",
		"",
	} {
		if got := localizeURL(raw, byRemote); got != raw {
			t.Errorf("localizeURL(%q) = %q, want it unchanged", raw, got)
		}
	}
}

func TestTunnelURL(t *testing.T) {
	got, err := tunnelURL("https://api.gagarin.cloud", "shop", "db", 5432)
	if err != nil {
		t.Fatal(err)
	}
	if want := "wss://api.gagarin.cloud/v1/projects/shop/resources/db/tunnel?port=5432"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	got, err = tunnelURL("http://127.0.0.1:8081", "shop", "db", 6379)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "ws://127.0.0.1:8081/") {
		t.Errorf("http base should become ws: %q", got)
	}

	if _, err := tunnelURL("ftp://x", "shop", "db", 1); err == nil {
		t.Error("a scheme that cannot upgrade should refuse")
	}
}

// A refusal on the handshake is still the API's own error, not websocket
// noise: the envelope on the failed upgrade response is what the caller sees.
func TestDialTunnelSurfacesTheEnvelope(t *testing.T) {
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"not_tunnelable","message":"openai is an external: there is nothing to tunnel to","fix_hint":"its values are gg resource secrets"}}`))
	})

	_, err := dialTunnel("shop", "openai", 443)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "[not_tunnelable]") {
		t.Errorf("want the envelope's code in the error, got %q", err)
	}
	if !strings.Contains(err.Error(), "hint:") {
		t.Errorf("want the hint carried through, got %q", err)
	}
}

// The whole local half, against a fake control plane that echoes: bytes go in
// a local connection, come back out of it, and a local hangup releases the
// websocket.
func TestPipeLocalCarriesBytes(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverDone := make(chan struct{})
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		if r.Header.Get("Authorization") != "Bearer gg_test_token" {
			t.Errorf("no credential on the handshake: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Query().Get("port") != "5432" {
			t.Errorf("port not carried: %q", r.URL.RawQuery)
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer ws.Close()
		for {
			kind, msg, err := ws.ReadMessage()
			if err != nil {
				return // the close frame, once the local side hangs up
			}
			if err := ws.WriteMessage(kind, msg); err != nil {
				return
			}
		}
	})

	ws, err := dialTunnel("shop", "db", 5432)
	if err != nil {
		t.Fatal(err)
	}

	local, far := net.Pipe()
	pipeDone := make(chan struct{})
	go func() {
		defer close(pipeDone)
		pipeLocal(ws, far)
	}()

	local.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := local.Write([]byte("SELECT 1")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	n, err := local.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "SELECT 1" {
		t.Errorf("echoed %q", got)
	}

	// Hanging up locally must end the pipe and tell the far side.
	local.Close()
	for name, ch := range map[string]chan struct{}{"pipe": pipeDone, "server": serverDone} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s did not end after the local hangup", name)
		}
	}
}
