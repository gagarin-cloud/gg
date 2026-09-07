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
}

type runFlagVars struct {
	f *runFlags
	*envFlagVars
}

// bindRunFlags is the deploy flags minus the volume — a run keeps nothing —
// plus --detach.
func bindRunFlags(fs *pflag.FlagSet) *runFlagVars {
	v := &runFlagVars{
		f: &runFlags{env: map[string]string{}},
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
	return v
}

func (v *runFlagVars) finish() (*runFlags, error) {
	env, err := v.envFlagVars.finish()
	if err != nil {
		return nil, err
	}
	v.f.env = env
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
// `gg status`. Over the platform's own ceiling on a run, so that the ceiling
// is what a stuck script hits, with its message, rather than this.
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
	body := map[string]any{
		"kind":   "job",
		"image":  target.ref,
		"digest": digest,
		"env":    f.env,
	}
	if f.size != "" {
		body["size"] = f.size
	}
	if len(f.deps) > 0 {
		body["deps"] = f.deps
	}
	var made struct {
		Name     string `json:"name"`
		Revision int    `json:"revision"`
	}
	if err := call("PUT", fmt.Sprintf("/v1/projects/%s/services/%s", project, name), body, &made); err != nil {
		return err
	}
	fmt.Printf("  revision %d submitted\n", made.Revision)
	if len(f.deps) > 0 {
		fmt.Printf("  reaching %s as well while it runs\n", strings.Join(f.deps, " and "))
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
	var out struct{ Logs string }
	if err := call("GET", "/v1/projects/"+project+"/services/"+name+"/logs", nil, &out); err == nil && out.Logs != "" {
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
