package main

// gg connect: a resource, on this machine, for as long as the command runs.
//
// Resources are deliberately not on the internet, and most days that is the
// whole point. Some days a human needs psql against the real database — a
// migration to babysit, a row to look at — and the honest choices are a
// sanctioned path or a workaround. This is the sanctioned path: a tunnel from
// a local port to the resource, opened through the gagarin API, gone when the
// command stops.
//
// The mechanics stay out of sight on purpose. What the user sees is the same
// connection variables `gg deps add` hands a service, rewritten to point at
// 127.0.0.1 — a local URL, not a lesson in networking. Under it, every TCP
// connection accepted locally becomes one websocket to the control plane,
// which pipes it to the resource's pod; nothing is exposed, nothing survives
// the command, and the resource is exactly as private afterwards as before.
//
// This is not `gg deps`. Connecting one service to another is a declaration
// on the graph and stays one; this verb is for the machine you are sitting
// at, which is not a service and never appears on the graph.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// tunnelWriteWait bounds any single websocket write; a peer that cannot take
// bytes for this long is gone, not slow. tunnelIdleWait is how long the far
// end may be silent before this side gives the connection up — the control
// plane pings well inside it, so only a dead path ever gets there.
const (
	tunnelWriteWait = 30 * time.Second
	tunnelIdleWait  = 90 * time.Second
	tunnelBuf       = 32 << 10
)

// tunnelPort is one port the resource answers on, and where it landed locally.
type tunnelPort struct {
	key     string // the env key that names it, e.g. PG_PORT, VECTORS_GRPC_PORT
	remote  int
	local   int
	primary bool
	ln      net.Listener
}

func cmdConnect(ref string, localPort int) error {
	project, name, refPort, err := parseService(ref)
	if err != nil {
		return err
	}
	if refPort != 0 {
		return fmt.Errorf("usage: gg connect PROJECT/RESOURCE [--port N]\n" +
			"  the local port is --port, not part of the name")
	}

	// The secrets endpoint answers three questions at once: does this resource
	// exist (with the platform's own refusal if not), what type is it, and
	// what does a caller need to connect — which is exactly what gets printed,
	// localised, once the tunnel is up.
	var sec struct {
		Resource string            `json:"resource"`
		Type     string            `json:"type"`
		Env      map[string]string `json:"env"`
	}
	path := fmt.Sprintf("/v1/projects/%s/resources/%s/secrets", project, name)
	if err := call("GET", path, nil, &sec); err != nil {
		return err
	}
	// The one type gg knows by name, and the one with no server on the far
	// end of any tunnel. Refused here rather than after a round trip, with
	// the hint pointing at the thing that IS there to read.
	if sec.Type == typeExternal {
		return fmt.Errorf("%s is an external: nothing runs here, so there is nothing to tunnel to\n"+
			"  hint: its values are `gg resource secrets %s/%s`", name, project, name)
	}

	tuns := tunnelPortsOf(envPrefix(name), sec.Env)
	if len(tuns) == 0 {
		return fmt.Errorf("%s publishes no port to tunnel to\n  hint: `gg status %s` says what it is", name, project)
	}

	// Local listeners before anything is promised. Each port prefers its own
	// number — psql's default just works — and falls back to whatever is
	// free; --port pins the primary and only the primary.
	defer func() {
		for _, t := range tuns {
			if t.ln != nil {
				t.ln.Close()
			}
		}
	}()
	for _, t := range tuns {
		want := 0
		if t.primary {
			want = localPort
		}
		if err := t.listen(want); err != nil {
			return err
		}
	}

	// One probe connection, opened and closed before anything is printed: it
	// proves the credential, the route and the running pod, so a resource
	// that is down refuses now with `[code] message` instead of later with a
	// database driver's own timeout prose.
	ws, err := dialTunnel(project, name, tuns[0].remote)
	if err != nil {
		return err
	}
	ws.Close()

	fmt.Printf("%s is a %s. The tunnel is up:\n\n", name, sec.Type)
	local := localizeEnv(sec.Env, envPrefix(name), tuns)
	keys := make([]string, 0, len(local))
	for k := range local {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %s=%s\n", k, local[k])
	}
	fmt.Printf(`
Those are live credentials, and anything on this machine can use them while
this command runs. Ctrl-C closes the tunnel and every connection through it.
`)

	for _, t := range tuns {
		go t.serve(project, name)
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	<-sigc
	fmt.Printf("\ntunnel closed\n")
	return nil
}

// tunnelPortsOf reads which ports the resource answers on out of its own
// connection variables — every key ending _PORT is one, and <PREFIX>_PORT is
// the primary. Derived rather than hard-coded per type, so a new resource
// type tunnels correctly the day it exists.
func tunnelPortsOf(prefix string, env map[string]string) []*tunnelPort {
	var out []*tunnelPort
	for k, v := range env {
		if !strings.HasSuffix(k, "_PORT") {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			continue
		}
		out = append(out, &tunnelPort{key: k, remote: n, primary: k == prefix+"_PORT"})
	}
	// Primary first, the rest in name order, so the probe and --port land on
	// the port the URL addresses.
	sort.Slice(out, func(i, j int) bool {
		if out[i].primary != out[j].primary {
			return out[i].primary
		}
		return out[i].key < out[j].key
	})
	return out
}

// listen binds the local half. A wanted port is honoured or refused — never
// silently moved, because the caller asked for that number to give to some
// tool's config. Without one, the resource's own number is tried first and an
// ephemeral port taken quietly when it is busy.
func (t *tunnelPort) listen(want int) error {
	if want > 0 {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", want))
		if err != nil {
			return fmt.Errorf("cannot listen on 127.0.0.1:%d: %v\n  hint: something already uses it; pick another --port, or omit it", want, err)
		}
		t.ln, t.local = ln, want
		return nil
	}
	if ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", t.remote)); err == nil {
		t.ln, t.local = ln, t.remote
		return nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("cannot listen on 127.0.0.1: %v", err)
	}
	t.ln, t.local = ln, ln.Addr().(*net.TCPAddr).Port
	return nil
}

// serve accepts local connections and gives each its own tunnel. Per-connection
// failures go to stderr and cost only that connection: the listener stays up,
// because a database being restarted mid-session is exactly when somebody is
// about to reconnect.
func (t *tunnelPort) serve(project, name string) {
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			return // the listener closed; the command is ending
		}
		go func() {
			ws, err := dialTunnel(project, name, t.remote)
			if err != nil {
				fmt.Fprintf(os.Stderr, "gg: %v\n", err)
				conn.Close()
				return
			}
			pipeLocal(ws, conn)
		}()
	}
}

// localizeEnv is the resource's connection variables, rewritten to arrive
// through the tunnel: hosts become 127.0.0.1, ports become their local ends,
// and URLs follow suit. Everything else — passwords, users, API keys — passes
// through untouched. No case per resource type: the suffix vocabulary is the
// platform's contract, and rewriting by suffix is what keeps this correct for
// a type that does not exist yet.
func localizeEnv(env map[string]string, prefix string, tuns []*tunnelPort) map[string]string {
	byKey := map[string]int{}
	byRemote := map[string]int{}
	for _, t := range tuns {
		byKey[t.key] = t.local
		byRemote[strconv.Itoa(t.remote)] = t.local
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		switch {
		case k == prefix+"_HOST":
			out[k] = "127.0.0.1"
		case byKey[k] != 0:
			out[k] = strconv.Itoa(byKey[k])
		case strings.HasSuffix(k, "_URL"):
			out[k] = localizeURL(v, byRemote)
		default:
			out[k] = v
		}
	}
	return out
}

// localizeURL points one URL at the tunnel, matching by the port in its host —
// the URL and the _PORT variable name the same listener, so the mapping is the
// same. A URL that does not parse, or whose port is not tunnelled, passes
// through unchanged rather than half-rewritten.
func localizeURL(raw string, byRemote map[string]int) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	local, ok := byRemote[u.Port()]
	if !ok {
		return raw
	}
	u.Host = "127.0.0.1:" + strconv.Itoa(local)
	return u.String()
}

// dialTunnel opens one tunnel connection. A refusal arrives as the API's
// ordinary error envelope on the handshake response, so the caller gets the
// same `[code] message` every other verb produces.
func dialTunnel(project, name string, remotePort int) (*websocket.Conn, error) {
	base, token, err := resolveAuth()
	if err != nil {
		return nil, err
	}
	wsURL, err := tunnelURL(base, project, name, remotePort)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	hdr.Set("User-Agent", clientName())
	dialer := websocket.Dialer{HandshakeTimeout: 30 * time.Second}
	ws, resp, err := dialer.Dial(wsURL, hdr)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			var wrap struct{ Error apiError }
			if json.Unmarshal(raw, &wrap) == nil && wrap.Error.Code != "" {
				return nil, wrap.Error
			}
		}
		return nil, fmt.Errorf("cannot open a tunnel to %s/%s: %w", project, name, err)
	}
	return ws, nil
}

// tunnelURL is the API base with the scheme a websocket wants.
func tunnelURL(base, project, name string, port int) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("control plane URL %q: %w", base, err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("control plane URL %q: expected http or https", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/v1/projects/%s/resources/%s/tunnel", project, name)
	u.RawQuery = "port=" + strconv.Itoa(port)
	return u.String(), nil
}

// pipeLocal carries one local connection's bytes both ways until either side
// ends, then ends the other.
func pipeLocal(ws *websocket.Conn, conn net.Conn) {
	defer ws.Close()
	defer conn.Close()

	// The control plane pings to keep the path alive; answering is gorilla's
	// default. What is added here is the deadline those pings refresh, so a
	// path that dies without a FIN releases the connection instead of holding
	// it forever.
	_ = ws.SetReadDeadline(time.Now().Add(tunnelIdleWait))
	ws.SetPingHandler(func(payload string) error {
		_ = ws.SetReadDeadline(time.Now().Add(tunnelIdleWait))
		return ws.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(tunnelWriteWait))
	})

	// Tunnel to local, in its own goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				// The far side saying why it ended is worth passing on; a
				// polite goodbye is not.
				var ce *websocket.CloseError
				if errors.As(err, &ce) && ce.Code != websocket.CloseNormalClosure && ce.Text != "" {
					fmt.Fprintf(os.Stderr, "gg: tunnel: %s\n", ce.Text)
				}
				break
			}
			_ = ws.SetReadDeadline(time.Now().Add(tunnelIdleWait))
			if _, err := conn.Write(msg); err != nil {
				break
			}
		}
		conn.Close()
	}()

	// Local to tunnel.
	buf := make([]byte, tunnelBuf)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			_ = ws.SetWriteDeadline(time.Now().Add(tunnelWriteWait))
			if werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = ws.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(tunnelWriteWait))
	ws.Close()
	<-done
}
