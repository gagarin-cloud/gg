package main

// Going back, and leaving.
//
// Two commands that exist for the same reason: an agent operates gagarin on
// your behalf, and you only hand that over to something you can undo and
// something you can walk away from. Neither is a new mechanism — a rollback is
// a deploy of a revision the platform already recorded, and an eject is the
// objects the reconciler already converges toward.

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ---- history --------------------------------------------------------------

type deployment struct {
	Revision     int       `json:"revision"`
	Image        string    `json:"image"`
	Digest       string    `json:"digest"`
	Port         int       `json:"port"`
	Public       bool      `json:"public"`
	Needs        []string  `json:"needs"`
	DeployedBy   string    `json:"deployed_by"`
	RestoredFrom *int      `json:"restored_from"`
	CreatedAt    time.Time `json:"created_at"`
}

func cmdHistory(service, project string) error {
	if project == "" {
		project = defaultName()
	}

	var out struct {
		Service     string       `json:"service"`
		Deployments []deployment `json:"deployments"`
	}
	if err := call("GET", "/v1/projects/"+project+"/services/"+service+"/deployments", nil, &out); err != nil {
		return err
	}
	if len(out.Deployments) == 0 {
		fmt.Printf("%s has not been deployed yet.\n", service)
		return nil
	}

	// The live one first, marked, because "which of these am I running" is the
	// question somebody reaching for a rollback is actually asking.
	fmt.Printf("%s — %d deploy%s, newest first\n\n", service,
		len(out.Deployments), plural(len(out.Deployments)))
	for i, d := range out.Deployments {
		marker := "  "
		if i == 0 {
			marker = "→ "
		}
		note := ""
		if d.RestoredFrom != nil {
			note = fmt.Sprintf("  (rolled back to %d)", *d.RestoredFrom)
		}
		fmt.Printf("%s%-4d %-28s %s%s\n", marker, d.Revision,
			shortRef(d.Image), ago(d.CreatedAt), note)
		if d.DeployedBy != "" {
			fmt.Printf("       by %s\n", d.DeployedBy)
		}
	}
	fmt.Printf("\nGo back one with `gg rollback %s`, or to a particular one with -to N.\n", service)
	return nil
}

// ---- rollback -------------------------------------------------------------

func cmdRollback(service, project string, to int) error {
	if project == "" {
		project = defaultName()
	}
	if to < 0 {
		return fmt.Errorf("%d is not a revision number; see `gg history SERVICE`", to)
	}

	body := map[string]any{}
	if to > 0 {
		body["to"] = to
	}
	var out struct {
		Revision     int    `json:"revision"`
		RestoredFrom int    `json:"restored_from"`
		Sentence     string `json:"sentence"`
	}
	if err := call("POST", "/v1/projects/"+project+"/services/"+service+"/rollback", body, &out); err != nil {
		return err
	}
	fmt.Println(out.Sentence)
	// History is append-only, and saying so here is what stops somebody
	// expecting the revision they just left to have disappeared.
	fmt.Printf("Revision %d is what is running now; %d is still in the history.\n",
		out.Revision, out.RestoredFrom)
	return nil
}

// ---- eject ----------------------------------------------------------------

func cmdEject(project, outPath string) error {
	if project == "" {
		project = defaultName()
	}

	// Raw rather than through call(): what comes back is a YAML file, not the
	// JSON envelope everything else here speaks.
	manifests, err := callYAML("GET", "/v1/projects/"+project+"/eject")
	if err != nil {
		return err
	}
	if outPath == "" {
		fmt.Print(manifests)
		return nil
	}
	// 0600: this file contains every service's environment in the clear, and a
	// world-readable copy of somebody's database password on a shared machine is
	// the sort of thing a trust feature must not create.
	if err := os.WriteFile(outPath, []byte(manifests), 0o600); err != nil {
		return err
	}
	fmt.Printf("Wrote %s (mode 0600 — it contains your environments in the clear).\n", outPath)
	fmt.Println("Apply it anywhere with: kubectl apply -f " + outPath)
	fmt.Println("Read the header first: the images are still in gagarin's registry.")
	return nil
}

// ---- small helpers --------------------------------------------------------

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// shortRef drops the registry and project prefix, which is identical on every
// line and crowds out the part that differs.
func shortRef(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 {
		return image[i+1:]
	}
	return image
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
