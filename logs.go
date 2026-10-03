package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// gg logs reads the logs route, which since core#64 answers from a store
// that keeps a week of every container's lines and falls back to the node
// when the store has nothing yet or does not answer.
//
// stdout is the lines and nothing else, so `gg logs shop/web | grep` sees only
// what the service printed. What gg has to say about them — that they came
// from the node only, and how to read the page before — goes to stderr.

type logsFlags struct {
	since, until, grep string
	limit              int
	previous           bool
}

type logsResp struct {
	Logs   string  `json:"logs"`
	Source string  `json:"source"`
	Next   *string `json:"next"`
	Notice string  `json:"notice"`
}

// logsQuery is the route's query string; only what was set, so the defaults
// stay the server's.
func logsQuery(f logsFlags) string {
	v := url.Values{}
	if f.since != "" {
		v.Set("since", f.since)
	}
	if f.until != "" {
		v.Set("until", f.until)
	}
	if f.limit > 0 {
		v.Set("limit", strconv.Itoa(f.limit))
	}
	if f.grep != "" {
		v.Set("q", f.grep)
	}
	if f.previous {
		v.Set("previous", "true")
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

func fetchLogs(project, service string, f logsFlags) (logsResp, error) {
	var out logsResp
	err := call("GET", "/v1/projects/"+project+"/services/"+service+"/logs"+logsQuery(f), nil, &out)
	return out, err
}

func cmdLogs(ref string, f logsFlags) error {
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	out, err := fetchLogs(project, service, f)
	if err != nil {
		return err
	}
	fmt.Print(out.Logs)
	if out.Logs != "" && !strings.HasSuffix(out.Logs, "\n") {
		fmt.Println()
	}
	if out.Notice != "" {
		fmt.Fprintf(os.Stderr, "gg: %s\n", out.Notice)
	}
	if out.Next != nil {
		fmt.Fprintf(os.Stderr, "older: %s\n", nextLogsCommandAt(project, service, f, *out.Next, time.Now()))
	}
	return nil
}

// nextLogsCommandAt is the invocation that reads the page before this one:
// the same flags, with --until moved back to where this page starts. --since
// stays if it was given, so paging stops at the start of the window that was
// asked about rather than running on to the end of the week — pinned to the
// time it meant at now, because "2h" pasted back an hour later is a window
// that starts an hour later, and past the --until the hint just set.
func nextLogsCommandAt(project, service string, f logsFlags, next string, now time.Time) string {
	args := []string{"gg logs", project + "/" + service}
	if f.since != "" {
		args = append(args, "--since", shellQuote(pinWhen(f.since, now)))
	}
	args = append(args, "--until", next)
	if f.limit > 0 {
		args = append(args, "-n", strconv.Itoa(f.limit))
	}
	if f.grep != "" {
		args = append(args, "--grep", shellQuote(f.grep))
	}
	return strings.Join(args, " ")
}

// shellQuote leaves plain words alone and single-quotes the rest, so the hint
// can be pasted back into a shell as it stands.
func shellQuote(s string) string {
	plain := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:+/", r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pinWhen is a --since or --until as the time it means at now: a duration back
// from now (30m, 6h, 2d) becomes RFC 3339, and anything else — already a time,
// or not something the engine will accept either — is left for it to judge.
// The engine reads the same forms (parseWhen in core's logs route).
func pinWhen(v string, now time.Time) string {
	var d time.Duration
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return v
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(v); err != nil || d < 0 {
			return v
		}
	}
	return now.Add(-d).UTC().Format(time.RFC3339Nano)
}
