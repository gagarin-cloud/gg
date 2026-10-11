package main

// `gg run`: an image that runs to completion.
//
// A job is the one thing gg waits for. Every other write here is submitted and
// left to `gg status` — a deploy has no natural end, and claiming one was the
// lie the old ninety-second wait told. A run has an end, and the whole reason
// somebody types this rather than `gg deploy` is to find out how it ended: a
// migration that failed is the thing they need to know before the next command,
// and its exit code is the thing a pipeline needs to branch on.

import (
	"fmt"
	"strings"
	"time"
	// The zone database, inside the binary: Windows has none for Go to read,
	// and without this every --tz but UTC is refused there.
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
	"github.com/spf13/pflag"
)

type runFlags struct {
	env  map[string]string
	size string
	// deps is edges to add, with the same rule a deploy's has: it adds and
	// never removes. The usual reason is a database the script migrates.
	deps []string
	// detach submits the run and returns, for a caller that has something
	// better to do than wait — or that wants the old asynchronous shape back.
	detach bool
	// timeout is the run's own limit, in whole seconds, when --timeout was
	// given; zero means it was not, and the job keeps the one it has.
	timeout int
	// schedule is a cron expression, and timeZone the IANA zone it is read in.
	// Empty means the flag was not given and the job keeps what it has.
	schedule string
	timeZone string
}

type runFlagVars struct {
	f        *runFlags
	fs       *pflag.FlagSet
	timeout  string
	schedule string
	tz       string
	*envFlagVars
}

// bindRunFlags is the deploy flags minus the volume — a run keeps nothing —
// plus --detach.
func bindRunFlags(fs *pflag.FlagSet) *runFlagVars {
	v := &runFlagVars{
		f:  &runFlags{env: map[string]string{}},
		fs: fs,
		envFlagVars: bindEnvFlags(fs,
			"set an env var K=V (repeatable)",
			"read KEY=VALUE lines from a file (repeatable; later\nfiles win, --env flags win over all files)"),
	}
	fs.StringVar(&v.f.size, "size", "", "how much CPU and memory: s (0.5 vCPU / 1 GB, shared),\n"+
		"m (1 vCPU / 2 GB, dedicated) or l (2 vCPU / 4 GB,\n"+
		"dedicated). Omit to keep the current size")
	fs.StringArrayVar(&v.f.deps, "deps", nil, "also let this job reach `NAME`, and hold its\n"+
		"connection variables if NAME is a resource (repeatable).\n"+
		"Adds to what it already reaches and never removes;\n"+
		"use \"gg deps rm\" to withdraw one")
	fs.BoolVar(&v.f.detach, "detach", false, "submit the run and return; \"gg status\" reports how it\nends")
	fs.StringVar(&v.timeout, "timeout", "", "stop the run if it has not finished after `DURATION`, e.g. 45s,\n"+
		"5m, 1h; at most 60m, the default. Counted from when the\n"+
		"run gets a machine, so pulling the image uses some of it\n"+
		"but waiting for a machine does not.\n"+
		"Omit to keep the job's current timeout")
	fs.StringVar(&v.schedule, "schedule", "", "run it on a schedule: a 5-field cron expression such as\n"+
		"\"0 3 * * *\", or @hourly, @daily, @weekly, @monthly,\n"+
		"@yearly. Nothing runs now and gg does not wait. A schedule\n"+
		"cannot be removed; destroy the job to stop it")
	fs.StringVar(&v.tz, "tz", "", "the time zone a schedule is read in, an IANA name such as\n"+
		"Europe/Berlin. Omit for UTC, or to keep the job's zone")
	return v
}

// The platform's bounds on a run's timeout, in seconds.
const (
	minRunTimeout = 1
	maxRunTimeout = 3600
)

// parseTimeout turns --timeout into whole seconds, refusing what the platform
// would: under a second, over an hour, or a fraction of a second.
func parseTimeout(s string) (int, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--timeout %q is not a duration; write it like 45s, 5m or 1h", s)
	}
	if d%time.Second != 0 {
		return 0, fmt.Errorf("--timeout %s is not a whole number of seconds", s)
	}
	secs := int(d / time.Second)
	if secs < minRunTimeout {
		return 0, fmt.Errorf("--timeout %s is under one second", s)
	}
	if secs > maxRunTimeout {
		return 0, fmt.Errorf("--timeout %s is over the 60m limit on a run", s)
	}
	return secs, nil
}

// parseSchedule checks a cron expression the way the control plane will, so a
// typo is refused before an image is resolved. It returns the expression to
// send. A CRON_TZ=/TZ= prefix is refused because the zone is --tz's business,
// and an empty one because a schedule cannot be removed: `gg run` means "run
// this", so a way to say "no schedule" would fire a run when all somebody
// wanted was to stop the schedule.
func parseSchedule(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("--schedule is empty, and a schedule cannot be removed\n" +
			"  hint: destroy the job and run it again without --schedule")
	}
	if strings.HasPrefix(s, "CRON_TZ=") || strings.HasPrefix(s, "TZ=") {
		return "", fmt.Errorf("--schedule %q sets a time zone inside the expression\n"+
			"  hint: leave it out and pass --tz Europe/Berlin", s)
	}
	if strings.HasPrefix(s, "@every") {
		return "", fmt.Errorf("--schedule %q is not supported\n"+
			"  hint: write 5 fields like \"*/5 * * * *\", or a macro such as @hourly", s)
	}
	if _, err := cron.ParseStandard(s); err != nil {
		return "", fmt.Errorf("--schedule %q is not a cron expression: %v\n"+
			"  hint: 5 fields (minute hour day month weekday) like \"0 3 * * *\", or @hourly, @daily, @weekly, @monthly, @yearly", s, err)
	}
	return s, nil
}

// parseTimeZone checks --tz against the zone database.
func parseTimeZone(s string) (string, error) {
	if s == "" || s == "Local" {
		return "", fmt.Errorf("--tz %q is not an IANA time zone name\n"+
			"  hint: e.g. Europe/Berlin or America/New_York; leave it out for UTC", s)
	}
	if _, err := time.LoadLocation(s); err != nil {
		return "", fmt.Errorf("--tz %q is not an IANA time zone name\n"+
			"  hint: e.g. Europe/Berlin or America/New_York; leave it out for UTC", s)
	}
	return s, nil
}

// nextRun is when a schedule next fires after now, read in zone ("" is UTC).
func nextRun(expr, zone string, now time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, err
	}
	loc := time.UTC
	if zone != "" {
		if loc, err = time.LoadLocation(zone); err != nil {
			return time.Time{}, err
		}
	}
	return sched.Next(now.In(loc)), nil
}

// runBody is the request that submits one run. timeout_seconds is sent only
// when --timeout was given: absent, the server keeps the job's current one.
func runBody(ref, digest string, f *runFlags) map[string]any {
	body := map[string]any{
		"kind":   "job",
		"image":  ref,
		"digest": digest,
		"env":    f.env,
	}
	if f.size != "" {
		body["size"] = f.size
	}
	if len(f.deps) > 0 {
		body["deps"] = f.deps
	}
	if f.timeout > 0 {
		body["timeout_seconds"] = f.timeout
	}
	if f.schedule != "" {
		body["schedule"] = f.schedule
	}
	if f.timeZone != "" {
		body["time_zone"] = f.timeZone
	}
	return body
}

func (v *runFlagVars) finish() (*runFlags, error) {
	env, err := v.envFlagVars.finish()
	if err != nil {
		return nil, err
	}
	v.f.env = env
	if v.timeout != "" {
		if v.f.timeout, err = parseTimeout(v.timeout); err != nil {
			return nil, err
		}
	}
	// Changed rather than non-empty, so --schedule "" is refused instead of
	// quietly meaning "not given".
	if v.fs.Changed("schedule") {
		if v.f.schedule, err = parseSchedule(v.schedule); err != nil {
			return nil, err
		}
	}
	if v.fs.Changed("tz") {
		if v.f.timeZone, err = parseTimeZone(v.tz); err != nil {
			return nil, err
		}
	}
	if len(v.f.deps) > 0 {
		if err := checkNames(v.f.deps); err != nil {
			return nil, err
		}
	}
	return v.f, nil
}

// runPoll is how often the run is asked about, and it widens with the run.
//
// Two seconds at the start, because that is under the time a pod takes to
// schedule and pull and most runs are short: a migration that ends in forty
// seconds is reported the moment it ends rather than up to a poll later. But
// a status read is a cluster read per service in the project, and holding two
// seconds for an hour is eighteen hundred of them to learn something that
// happens once. After a minute the answer is no longer imminent, so the
// question is asked less often.
func runPoll(elapsed time.Duration) time.Duration {
	switch {
	case elapsed < time.Minute:
		return 2 * time.Second
	case elapsed < 5*time.Minute:
		return 5 * time.Second
	default:
		return 15 * time.Second
	}
}

// runWait is how long gg waits before giving up on a run and leaving it to
// `gg status`. Over any timeout a run can be given, so that the timeout is
// what a stuck script hits, with its message, rather than this.
const runWait = 65 * time.Minute

func cmdRun(ref, image string, f *runFlags) error {
	project, name, err := parseJob(ref)
	if err != nil {
		return err
	}
	repo, tag, err := parseRepoTag(image, project)
	if err != nil {
		return err
	}
	target, err := resolveTarget(project, repo, tag)
	if err != nil {
		return err
	}
	digest := target.digest
	if digest == "" {
		digest = imageDigest(target.ref)
	}

	fmt.Printf("→ running %s as job %s\n", target.short(), name)
	body := runBody(target.ref, digest, f)
	var made struct {
		Name     string `json:"name"`
		Revision int    `json:"revision"`
		Schedule string `json:"schedule"`
		TimeZone string `json:"time_zone"`
	}
	if err := call("PUT", fmt.Sprintf("/v1/projects/%s/services/%s", project, name), body, &made); err != nil {
		return err
	}
	fmt.Printf("  revision %d submitted\n", made.Revision)
	if len(f.deps) > 0 {
		fmt.Printf("  reaching %s as well while it runs\n", strings.Join(f.deps, " and "))
	}
	// A scheduled job runs nothing now, so there is no run to wait for —
	// whether or not --detach was given.
	if made.Schedule != "" {
		zone := made.TimeZone
		if zone == "" {
			zone = "UTC"
		}
		next, err := nextRun(made.Schedule, made.TimeZone, time.Now())
		if err != nil || next.IsZero() {
			fmt.Printf("  scheduled %q (%s)\n", made.Schedule, zone)
		} else {
			fmt.Printf("  scheduled %q (%s), next run %s\n", made.Schedule, zone,
				next.Format("2006-01-02 15:04 MST"))
		}
		fmt.Printf("\n`gg status %s` reports each run, and `gg logs %s/%s` prints what it wrote.\n",
			project, project, name)
		return nil
	}
	if f.detach {
		fmt.Printf("\n`gg status %s` reports how it ends, and `gg logs %s/%s` prints what it wrote.\n",
			project, project, name)
		return nil
	}
	return waitForRun(project, name, made.Revision)
}

// waitForRun follows one revision of a job to its end, prints what it wrote,
// and turns how it ended into gg's own exit.
//
// The revision is what is waited on, not the job: a status read that shows
// an older run still finishing must not be mistaken for this one, and a
// re-run started by somebody else meanwhile must not be either.
func waitForRun(project, name string, revision int) error {
	started := time.Now()
	giveUp := started.Add(runWait)
	last := ""
	var run *runState
	var message string
	for {
		var st statusResp
		if err := call("GET", "/v1/projects/"+project+"/status", nil, &st); err != nil {
			return err
		}
		var s *serviceStatus
		for i := range st.Services {
			if st.Services[i].Name == name {
				s = &st.Services[i]
			}
		}
		if s == nil {
			return fmt.Errorf("%s is no longer in project %s; it was destroyed while it was running", name, project)
		}
		run, message = s.Actual.Run, s.Actual.Message
		phase := "pending"
		if run != nil && run.Revision == revision {
			phase = run.Phase
		}
		line := "  " + phase
		if message != "" && (phase == "pending" || phase == "failed") {
			line += ": " + message
		}
		// The whole line, not the phase, is what must have changed to be worth
		// reprinting. A run that goes from "pending: ContainerCreating" to
		// "pending: CreateContainerError" has not changed phase and has told
		// you the only thing you needed to know — that waiting will not fix
		// it. Watching a job sit at ContainerCreating for ten minutes while
		// the cluster had already said why is how this was found.
		if line != last {
			fmt.Println(line)
			last = line
		}
		if run != nil && run.Revision == revision && (phase == "done" || phase == "failed") {
			break
		}
		if time.Now().After(giveUp) {
			return fmt.Errorf("%s is still %s after %s; `gg status %s` reports how it ends",
				name, phase, runWait, project)
		}
		time.Sleep(runPoll(time.Since(started)))
	}

	// What it wrote, whatever happened. The failure case is the one where
	// this matters most, and it is printed before the verdict so the last
	// line on the screen is the one that says what to do.
	//
	// Only this run's: the store lags a job that has just finished, and asked
	// for "the latest lines" of a job that ran before, it answers with the
	// earlier run's. Bounded by this run's start, a store that has nothing yet
	// is what sends the engine to the node, which has it all.
	var f logsFlags
	if run.StartedAt != nil {
		f.since = run.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if out, err := fetchLogs(project, name, f); err == nil && out.Logs != "" {
		fmt.Println()
		fmt.Print(out.Logs)
		if !strings.HasSuffix(out.Logs, "\n") {
			fmt.Println()
		}
		fmt.Println()
	}

	if run.Phase == "done" {
		fmt.Printf("%s finished: revision %d, exit 0\n", name, revision)
		return nil
	}
	code := 1
	if run.ExitCode != nil && *run.ExitCode != 0 {
		code = *run.ExitCode
	}
	return &exitError{code: code, msg: fmt.Sprintf("%s failed: %s\n  `gg logs %s/%s` prints what it wrote; fix and run again",
		name, message, project, name)}
}
