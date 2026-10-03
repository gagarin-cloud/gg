package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
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
		fmt.Fprintf(os.Stderr, "older: %s\n", nextLogsCommand(project, service, f, *out.Next))
	}
	return nil
}

// nextLogsCommand is the invocation that reads the page before this one: the
// same flags, with --until moved back to where this page starts. --since stays
// if it was given, so paging stops at the start of the window that was asked
// about rather than running on to the end of the week.
func nextLogsCommand(project, service string, f logsFlags, next string) string {
	args := []string{"gg logs", project + "/" + service}
	if f.since != "" {
		args = append(args, "--since", shellQuote(f.since))
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
