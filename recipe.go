package main

// Recipes: a project, published so that anybody holding its link can deploy
// it again into a project of their own.
//
// A recipe is a snapshot, not a pointer. `gg recipe snap` reads what a project
// runs — services, jobs, resources, the edges between them, every image pinned
// to its digest — and the platform copies the images into the recipe's own
// space, so nothing the project does afterwards changes what the recipe
// installs. Installing copies them again, into the new project's space:
// Gagarin runs an image only in the project that holds it, and a recipe gets no
// exception to that.
//
// Install always creates a project. There is no installing into one you have,
// because a recipe that lands on top of somebody's services is a merge, and
// the platform does merges only as an update to something it installed itself.
//
// Secrets are never part of one. Each external resource the project holds
// becomes an input, published as its variable names; whoever installs it
// names an env file per input with --input, and gg reads that file when the
// platform is ready for it — after the images are copied, not before — and
// never prints a value from it, on success or failure. Values go out in one
// request body and nowhere else.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// deployLinkBase is where a recipe's deploy page lives. The id is the whole
// address: a name is unique only within one account. deployLinkPath is the
// same without the scheme, which is how a link comes back after a chat client
// or a human has had it.
const (
	deployLinkPath = "my.gagarin.cloud/deploy/"
	deployLinkBase = "https://" + deployLinkPath
)

func deployLink(id string) string { return deployLinkBase + id }

// recipePoll is how often install and update look at the copy. A variable so
// tests do not spend seconds of wall clock on it.
var recipePoll = 3 * time.Second

// ---- what the platform answers ------------------------------------------

// recipeMoney is what a recipe costs whoever installs it, a month, in
// micro-dollars, derived by the platform from the tariff.
type recipeMoney struct {
	Total   int64    `json:"total"`
	Running int64    `json:"running"`
	Storage int64    `json:"storage"`
	Notes   []string `json:"notes"`
}

func (m recipeMoney) line() string {
	s := prose(m.Total) + "/month"
	if m.Running != 0 && m.Storage != 0 {
		s += fmt.Sprintf(" (%s running, %s storage)", prose(m.Running), prose(m.Storage))
	}
	return s
}

// recipeInput is one thing an installer supplies: an external resource, by
// name, and the variables its file has to hold. Names only; a recipe never
// carries a value.
type recipeInput struct {
	Name string   `json:"name"`
	Vars []string `json:"vars"`
}

type recipeManifest struct {
	Schema   int `json:"schema"`
	Services []struct {
		Name         string   `json:"name"`
		Repo         string   `json:"repo"`
		Port         int      `json:"port"`
		Size         string   `json:"size"`
		VolumePath   string   `json:"volume_path"`
		VolumeSizeGB int      `json:"volume_size_gb"`
		Deps         []string `json:"deps"`
		Public       bool     `json:"public"`
	} `json:"services"`
	Jobs []struct {
		Name     string   `json:"name"`
		Repo     string   `json:"repo"`
		Schedule string   `json:"schedule"`
		TimeZone string   `json:"time_zone"`
		Size     string   `json:"size"`
		Deps     []string `json:"deps"`
	} `json:"jobs"`
	Resources []struct {
		Name       string   `json:"name"`
		Type       string   `json:"type"`
		Size       string   `json:"size"`
		StorageGB  int      `json:"storage_gb"`
		Extensions []string `json:"extensions"`
	} `json:"resources"`
	Inputs []recipeInput `json:"inputs"`
}

type recipeResp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Official  bool   `json:"official"`
	Publisher *struct {
		GitHub string `json:"github"`
	} `json:"publisher"`
	SourceURL string         `json:"source_url"`
	Latest    string         `json:"latest"`
	Version   string         `json:"version"`
	State     string         `json:"state"`
	Notes     textOrList     `json:"notes"`
	Manifest  recipeManifest `json:"manifest"`
	Inputs    []recipeInput  `json:"inputs"`
	Monthly   recipeMoney    `json:"monthly_micro_usd"`
}

// inputs is what an installer must supply. The top-level list is the one the
// platform means for this; the manifest's is the same thing, read when an
// answer leaves the first out.
func (r recipeResp) inputs() []recipeInput {
	if r.Inputs != nil {
		return r.Inputs
	}
	return r.Manifest.Inputs
}

// textOrList reads a field that is a sentence or a list of them. A recipe's
// notes are the publisher's text; nothing here depends on which shape the
// platform chose, so neither is worth failing a read over.
type textOrList []string

func (t *textOrList) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if s != "" {
			*t = textOrList{s}
		}
		return nil
	}
	var l []string
	if err := json.Unmarshal(b, &l); err != nil {
		return err
	}
	*t = l
	return nil
}

type recipeSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Latest        string `json:"latest"`
	VersionsCount int    `json:"versions_count"`
	Copying       bool   `json:"copying"`
}

// projectRecipe is what a project says about the recipe it was installed from,
// and where an install or update of it stands.
type projectRecipe struct {
	Recipe struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"recipe"`
	Op      string `json:"op"`
	State   string `json:"state"`
	Version string `json:"version"`
	Error   string `json:"error"`
}

// ---- finding one -------------------------------------------------------

func myRecipes() ([]recipeSummary, error) {
	var out struct {
		Recipes []recipeSummary `json:"recipes"`
	}
	err := call("GET", "/v1/recipes", nil, &out)
	return out.Recipes, err
}

// resolveRecipe turns a name of yours, or anybody's id, into the id.
//
// A name is looked up among your own recipes, because that is the only place
// it means anything. Something shaped like an id is an id unless one of your
// recipes happens to carry it as its name — an 8-letter name is both — and
// when the list cannot be read at all, a signed-out reader holding a deploy
// link, it is an id too.
func resolveRecipe(ref string) (string, error) {
	idShaped := recipeIDRe.MatchString(ref)
	if idShaped && !nameRe.MatchString(ref) {
		return ref, nil
	}
	mine, err := myRecipes()
	if err != nil {
		if idShaped {
			return ref, nil
		}
		return "", err
	}
	for _, r := range mine {
		if r.Name == ref || r.ID == ref {
			return r.ID, nil
		}
	}
	if idShaped {
		return ref, nil
	}
	return "", fmt.Errorf("you have no recipe called %s\n  hint: gg recipe ls lists yours; somebody else's is named by its id, the end of its deploy link", ref)
}

// fetchRecipe reads a recipe, or one version of it, and keeps the bytes for
// `--format json`, which prints the answer as the platform sent it.
func fetchRecipe(id, version string) (recipeResp, json.RawMessage, error) {
	path := "/v1/recipes/" + id
	if version != "" {
		path += "/versions/" + version
	}
	var raw json.RawMessage
	var r recipeResp
	if err := callPublic("GET", path, &raw); err != nil {
		return r, nil, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return r, nil, err
	}
	return r, raw, nil
}

// ---- snap --------------------------------------------------------------

type snapFlags struct {
	version, as, source, notes string
	dryRun                     bool
}

type snapResp struct {
	Recipe struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Exists bool   `json:"exists"`
	} `json:"recipe"`
	Previous string `json:"previous"`
	Floor    string `json:"floor"`
	Version  string `json:"version"`
	Changes  []struct {
		Level string `json:"level"`
		Path  string `json:"path"`
		What  string `json:"what"`
	} `json:"changes"`
	Notes     []string    `json:"notes"`
	Monthly   recipeMoney `json:"monthly_micro_usd"`
	State     string      `json:"state"`
	Next      string      `json:"next"`
	DeployURL string      `json:"deploy_url"`
}

// cmdRecipeSnap publishes a project, or with --dry-run says what publishing
// it would mint. The two answers are the same shape, so they print the same
// way and a dry run is an honest preview of the real one.
func cmdRecipeSnap(ref string, f snapFlags) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	if f.version != "" {
		if err := checkVersion(f.version); err != nil {
			return err
		}
	}
	if f.as != "" && !nameRe.MatchString(f.as) {
		return fmt.Errorf("%q is not a usable recipe name: %s", f.as, nameShape)
	}
	body := map[string]any{}
	if f.as != "" {
		body["name"] = f.as
	}
	if f.version != "" {
		body["version"] = f.version
	}
	if f.source != "" {
		body["source_url"] = f.source
	}
	if f.notes != "" {
		body["notes"] = f.notes
	}
	if f.dryRun {
		body["dry_run"] = true
	}
	var out snapResp
	if err := call("POST", "/v1/projects/"+project+"/snap", body, &out); err != nil {
		return err
	}

	if f.dryRun {
		fmt.Printf("Dry run: nothing was published.\n\n")
	}
	if out.Previous == "" {
		fmt.Printf("%s %s, its first version\n", out.Recipe.Name, out.Version)
	} else {
		fmt.Printf("%s %s, after %s\n", out.Recipe.Name, out.Version, out.Previous)
	}
	if out.Floor != "" {
		fmt.Printf("  floor %s: the lowest version these changes allow\n", out.Floor)
	}
	if len(out.Changes) > 0 {
		fmt.Printf("\nwhat changed\n")
		for _, c := range out.Changes {
			fmt.Printf("  %-5s  %s  %s\n", c.Level, c.Path, c.What)
		}
	}
	if len(out.Notes) > 0 {
		fmt.Printf("\nnotes\n")
		for _, n := range out.Notes {
			fmt.Printf("  %s\n", n)
		}
	}
	fmt.Printf("\nWhoever installs it pays about %s.\n", out.Monthly.line())
	for _, n := range out.Monthly.Notes {
		fmt.Printf("  %s\n", n)
	}

	if f.dryRun {
		fmt.Printf("\nWithout --dry-run this publishes %s %s.\n", out.Recipe.Name, out.Version)
		return nil
	}
	fmt.Printf("\npublished %s %s (id %s)\n", out.Recipe.Name, out.Version, out.Recipe.ID)
	if out.State == "copying" {
		fmt.Println("The platform is copying its images into the recipe; the link installs it once that is done.")
	}
	if out.Next != "" {
		fmt.Println(out.Next)
	}
	link := out.DeployURL
	if link == "" && out.Recipe.ID != "" {
		link = deployLink(out.Recipe.ID)
	}
	if link != "" {
		fmt.Printf("\n  %s\n", link)
	}
	return nil
}

// ---- show, ls, rm ------------------------------------------------------

func cmdRecipeShow(s, format string) error {
	ref, version, err := parseRecipeRef(s)
	if err != nil {
		return err
	}
	if format != "" && format != "text" && format != "json" {
		return fmt.Errorf("unknown format %q\n  hint: text or json", format)
	}
	id, err := resolveRecipe(ref)
	if err != nil {
		return err
	}
	r, raw, err := fetchRecipe(id, version)
	if err != nil {
		return err
	}
	if format == "json" {
		var b bytes.Buffer
		if err := json.Indent(&b, raw, "", "  "); err != nil {
			return err
		}
		fmt.Println(b.String())
		return nil
	}
	printRecipe(r)
	return nil
}

func printRecipe(r recipeResp) {
	fmt.Printf("%s %s  (id %s)\n", r.Name, r.Version, r.ID)
	switch {
	case r.Official:
		fmt.Println("  official: published by Gagarin")
	case r.Publisher != nil && r.Publisher.GitHub != "":
		fmt.Printf("  published by github.com/%s\n", r.Publisher.GitHub)
	default:
		fmt.Println("  published by an account with no GitHub login")
	}
	if r.Latest != "" && r.Latest != r.Version {
		fmt.Printf("  the latest is %s\n", r.Latest)
	}
	if r.State != "" && r.State != "published" {
		fmt.Printf("  this version is %s\n", r.State)
	}
	if r.SourceURL != "" {
		fmt.Printf("  source %s\n", r.SourceURL)
	}
	for _, n := range r.Notes {
		fmt.Printf("  %s\n", n)
	}
	fmt.Printf("  about %s to run\n", r.Monthly.line())
	for _, n := range r.Monthly.Notes {
		fmt.Printf("    %s\n", n)
	}

	m := r.Manifest
	inputs := r.inputs()
	w := 0
	widen := func(n string) {
		if len(n) > w {
			w = len(n)
		}
	}
	for _, s := range m.Services {
		widen(s.Name)
	}
	for _, j := range m.Jobs {
		widen(j.Name)
	}
	for _, x := range m.Resources {
		widen(x.Name)
	}
	for _, in := range inputs {
		widen(in.Name)
	}
	row := func(name string, parts []string, deps []string) {
		line := "  " + name + strings.Repeat(" ", w-len(name))
		if len(parts) > 0 {
			line += "  " + strings.Join(parts, "  ")
		}
		if len(deps) > 0 {
			line += "  → " + strings.Join(deps, ", ")
		}
		fmt.Println(line)
	}
	sized := func(parts []string, size string) []string {
		if size != "" {
			parts = append(parts, "size "+size)
		}
		return parts
	}

	if len(m.Services) > 0 {
		fmt.Printf("\nservices\n")
		for _, s := range m.Services {
			var parts []string
			if s.Port != 0 {
				parts = append(parts, fmt.Sprintf(":%d", s.Port))
			}
			parts = sized(parts, s.Size)
			if s.Public {
				parts = append(parts, "public")
			}
			if s.VolumePath != "" {
				parts = append(parts, fmt.Sprintf("volume %s %d GB", s.VolumePath, s.VolumeSizeGB))
			}
			row(s.Name, parts, s.Deps)
		}
	}
	if len(m.Jobs) > 0 {
		fmt.Printf("\njobs\n")
		for _, j := range m.Jobs {
			var parts []string
			if j.Schedule != "" {
				sched := fmt.Sprintf("%q", j.Schedule)
				if j.TimeZone != "" {
					sched += " " + j.TimeZone
				}
				parts = append(parts, sched)
			}
			row(j.Name, sized(parts, j.Size), j.Deps)
		}
	}
	if len(m.Resources) > 0 {
		fmt.Printf("\nresources\n")
		for _, x := range m.Resources {
			parts := sized([]string{x.Type}, x.Size)
			if x.StorageGB > 0 {
				parts = append(parts, fmt.Sprintf("%d GB", x.StorageGB))
			}
			if len(x.Extensions) > 0 {
				parts = append(parts, strings.Join(x.Extensions, ", "))
			}
			row(x.Name, parts, nil)
		}
	}
	if len(inputs) > 0 {
		fmt.Printf("\ninputs: an env file each, given on install as --input NAME=FILE\n")
		for _, in := range inputs {
			row(in.Name, []string{strings.Join(in.Vars, ", ")}, nil)
		}
	}
	fmt.Printf("\ndeploy  %s\n", deployLink(r.ID))
}

func cmdRecipeList() error {
	mine, err := myRecipes()
	if err != nil {
		return err
	}
	if len(mine) == 0 {
		fmt.Println("no recipes yet; gg recipe snap PROJECT publishes one")
		return nil
	}
	for _, r := range mine {
		latest := r.Latest
		if latest == "" {
			latest = "-"
		}
		line := fmt.Sprintf("%-30s  %-8s  %-10s  %d version%s",
			r.Name, r.ID, latest, r.VersionsCount, plural(r.VersionsCount))
		// A snap in flight is a version that cannot be installed yet, which is
		// the thing somebody who just published and sent the link needs to know.
		if r.Copying {
			line += "  copying"
		}
		fmt.Println(line)
	}
	return nil
}

// cmdRecipeRemove withdraws a version, or the whole recipe. It is gated the
// way every removal is — the platform mails the account owner and answers
// approval_required until somebody clicks — and it warns first, for the
// reason `gg deps rm` does: the email lands in somebody else's inbox, and the
// person typing this is the last one who can still stop.
func cmdRecipeRemove(s string) error {
	ref, version, err := parseRecipeRef(s)
	if err != nil {
		return err
	}
	id, err := resolveRecipe(ref)
	if err != nil {
		return err
	}
	path := "/v1/recipes/" + id
	if version != "" {
		path += "/versions/" + version
		fmt.Printf("Withdrawing %s %s.\n", ref, version)
		fmt.Printf("\n  Nobody can install that version any more. Projects already running it\n")
		fmt.Printf("  keep what they have.\n")
	} else {
		fmt.Printf("Withdrawing the recipe %s, and every version of it.\n", ref)
		fmt.Printf("\n  Its deploy link stops installing anything. Projects already installed\n")
		fmt.Printf("  from it keep what they have, and have nothing to update to.\n")
	}
	printApprovalNote()
	if err := call("DELETE", path, nil, nil); err != nil {
		return err
	}
	if version != "" {
		fmt.Printf("%s %s withdrawn\n", ref, version)
		return nil
	}
	fmt.Printf("%s withdrawn; its link no longer installs anything\n", ref)
	return nil
}

// ---- inputs ------------------------------------------------------------

// parseInputFlags reads the --input NAME=FILE flags into name → path. Nothing
// is read from the files here. The errors never repeat what was typed after
// the name: a value pasted where a path belongs is a credential, and saying it
// back puts it in one more transcript.
func parseInputFlags(flags []string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range flags {
		name, file, ok := strings.Cut(f, "=")
		if !ok || name == "" || file == "" {
			return nil, fmt.Errorf("--input expects NAME=FILE\n  hint: --input openai=.env.openai; the values go in the file, never on the command line")
		}
		if !nameRe.MatchString(name) {
			return nil, fmt.Errorf("--input %q: an input is named like a resource: %s", name, nameShape)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("--input %s is given twice\n  hint: one file per input; put every variable it needs in that file", name)
		}
		out[name] = file
	}
	return out, nil
}

// checkInputs refuses before anything is created: an input with no file, or a
// file for an input there is no such thing as. Names only.
func checkInputs(what string, declared []recipeInput, given map[string]string) error {
	known := map[string]bool{}
	var names []string
	for _, in := range declared {
		known[in.Name] = true
		names = append(names, in.Name)
	}
	var unknown []string
	for n := range given {
		if !known[n] {
			unknown = append(unknown, n)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		if len(declared) == 0 {
			return fmt.Errorf("%s takes no inputs, and --input names %s\n  hint: leave the --input off",
				what, strings.Join(unknown, " and "))
		}
		return fmt.Errorf("%s has no input called %s\n  hint: it takes %s",
			what, strings.Join(unknown, " or "), strings.Join(names, ", "))
	}
	var missing, hints []string
	for _, in := range declared {
		if _, ok := given[in.Name]; !ok {
			missing = append(missing, in.Name)
			hints = append(hints, fmt.Sprintf("--input %s=FILE (%s)", in.Name, strings.Join(in.Vars, ", ")))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s needs an env file for each of its inputs, and has none for %s\n  hint: %s; a FILE is KEY=VALUE lines",
			what, strings.Join(missing, ", "), strings.Join(hints, " "))
	}
	return nil
}

// statInputs checks each file is there, without reading it, so that a typo
// is answered before a project is created rather than after its images are
// copied. The path is not repeated, for the reason parseInputFlags gives.
func statInputs(given map[string]string) error {
	for _, name := range sortedKeys(given) {
		fi, err := os.Stat(given[name])
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("--input %s: there is no file at the path given\n  hint: the part after = is the path to a KEY=VALUE file", name)
		case err != nil:
			return fmt.Errorf("--input %s: the file cannot be read", name)
		case fi.IsDir():
			return fmt.Errorf("--input %s: the path given is a directory, not a KEY=VALUE file", name)
		}
	}
	return nil
}

// readInputs reads every file, at the moment the platform will take them.
// parseEnvFile's errors name a line, never its content.
func readInputs(given map[string]string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	for _, name := range sortedKeys(given) {
		vals, err := parseEnvFile(given[name])
		if err != nil {
			return nil, fmt.Errorf("--input %s: %w", name, err)
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("--input %s: the file holds no KEY=VALUE lines", name)
		}
		out[name] = vals
	}
	return out, nil
}

// copyPollFailures is how many polls in a row may fail to reach the platform
// before the wait gives up. A copy takes a minute, and a laptop's network
// drops for a second; one lost GET is not a reason to abandon an install that
// is still going on the other side.
const copyPollFailures = 3

// waitForRecipeCopy follows a project's install or update until its images are
// copied, or the copy failed. No deadline of its own: the copy job retries and
// then gives up, and says so in the row this reads, so the wait ends when the
// copy does.
//
// An answer from the platform that refuses — a credential revoked meanwhile,
// a project gone — ends the wait at once; only not getting an answer is
// retried, and only copyPollFailures times in a row.
func waitForRecipeCopy(project string) error {
	failures := 0
	for {
		var st projectRecipe
		if err := call("GET", "/v1/projects/"+project+"/recipe", nil, &st); err != nil {
			var ae apiError
			failures++
			if errors.As(err, &ae) || failures >= copyPollFailures {
				return err
			}
			time.Sleep(recipePoll)
			continue
		}
		failures = 0
		switch st.State {
		case "copied", "installed":
			return nil
		case "failed":
			why := st.Error
			if why == "" {
				why = "the platform gave no reason"
			}
			return fmt.Errorf("copying the images into %s failed: %s\n  hint: run the same command again to try the copy again", project, why)
		}
		time.Sleep(recipePoll)
	}
}

// ---- install -----------------------------------------------------------

type installResp struct {
	Recipe struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"recipe"`
	Install struct {
		State   string `json:"state"`
		Version string `json:"version"`
		Error   string `json:"error"`
	} `json:"install"`
	Monthly recipeMoney       `json:"monthly_micro_usd"`
	Next    string            `json:"next"`
	URLs    map[string]string `json:"urls"`
}

// finishWithInputs runs one of the platform's two-step verbs — install, or
// update — to its end, and returns the answer that ended it.
//
// The first POST carries body and no inputs: it asks. The platform answers 200
// when it did the whole thing there and then, 202 when it has images to copy
// first, and inputs_missing when the images are already in place and the only
// thing it lacks is what the files hold. The last two are the same moment from
// here: the input files are read now, not before, and sent in one more POST
// with the same body. inputs_missing in answer to *that* POST is a refusal
// like any other, because the files were sent and found wanting.
//
// copied says the caller already knows the images are there — a run carrying
// on in a project an earlier run left copied — and the ask is skipped: the
// platform would only answer it inputs_missing.
func finishWithInputs[T any](project, path string, body map[string]any, given map[string]string, copied bool) (T, error) {
	var out T
	if !copied {
		code, err := callStatus("POST", path, body, &out)
		var ae apiError
		switch {
		case err == nil && code != http.StatusAccepted:
			return out, nil
		case err == nil:
			fmt.Println("copying images…")
			if err := waitForRecipeCopy(project); err != nil {
				return out, err
			}
		case errors.As(err, &ae) && ae.Code == "inputs_missing" && len(given) > 0:
		default:
			return out, err
		}
	}
	inputs, err := readInputs(given)
	if err != nil {
		return out, fmt.Errorf("%w\n  %s holds the copied images; run the same command again once the file is fixed", err, project)
	}
	finish := maps.Clone(body)
	if len(inputs) > 0 {
		finish["inputs"] = inputs
	}
	// A fresh value: the ask's 202 body has fields the final answer need not
	// repeat, and decoding on top of it would keep them.
	out = *new(T)
	if err := call("POST", path, finish, &out); err != nil {
		return out, fmt.Errorf("%w\n  %s holds the copied images; run the same command again once that is fixed", err, project)
	}
	return out, nil
}

// cmdRecipeInstall creates a project and installs a recipe into it, in the
// platform's two steps: ask, which starts the copy and takes no inputs, then —
// once the images are in the new project's space — ask again with them.
//
// Everything that can be refused without creating anything is refused first:
// a missing or unknown input, a file that is not there. A project with nothing
// in it is cheap, but it is still a name taken and a line in `gg projects`.
func cmdRecipeInstall(recipeArg, projectArg string, inputFlags []string) error {
	ref, version, err := parseRecipeRef(recipeArg)
	if err != nil {
		return err
	}
	name, err := parseProject(projectArg)
	if err != nil {
		return err
	}
	given, err := parseInputFlags(inputFlags)
	if err != nil {
		return err
	}
	id, err := resolveRecipe(ref)
	if err != nil {
		return err
	}
	r, _, err := fetchRecipe(id, version)
	if err != nil {
		return err
	}
	if err := checkInputs(r.Name, r.inputs(), given); err != nil {
		return err
	}
	if err := statInputs(given); err != nil {
		return err
	}
	// Pinned to the version just checked, so the inputs asked for are the
	// inputs of what gets installed, even if a newer one is published meanwhile.
	if version == "" {
		version = r.Version
	}

	fmt.Printf("→ installing %s %s into a new project, %s\n", r.Name, version, name)
	copied := false
	p, err := createProject(name)
	if err != nil {
		// Run again after a failed copy or a refused input, the project is
		// there already, holding this recipe's install; that is the one case
		// to carry on in rather than refuse.
		var st projectRecipe
		if call("GET", "/v1/projects/"+name+"/recipe", nil, &st) != nil || st.Recipe.ID != r.ID {
			return err
		}
		// Pinned to the version in flight, not the one just fetched: the
		// platform holds one install at a time, and asking for a version
		// published since the first run would be refused as a second one. The
		// inputs are checked again when that changes what is being installed.
		if st.Version != "" && st.Version != version {
			version = st.Version
			if r, _, err = fetchRecipe(id, version); err != nil {
				return err
			}
			if err := checkInputs(r.Name, r.inputs(), given); err != nil {
				return err
			}
		}
		copied = st.State == "copied"
		fmt.Printf("project %s exists, and is installing %s %s; carrying on\n", name, r.Name, version)
	} else {
		printCreated(p)
	}
	fmt.Printf("  about %s once it runs\n", r.Monthly.line())

	ask := map[string]any{"recipe": r.ID}
	if version != "" {
		ask["version"] = version
	}
	done, err := finishWithInputs[installResp](name, "/v1/projects/"+name+"/recipe", ask, given, copied)
	if err != nil {
		return err
	}
	if done.Install.Version != "" {
		version = done.Install.Version
	}
	fmt.Printf("installed %s %s into %s\n", r.Name, version, name)

	var st statusResp
	if err := call("GET", "/v1/projects/"+name+"/status", nil, &st); err != nil {
		return err
	}
	printStatusTable(st)
	if len(done.URLs) > 0 {
		fmt.Println()
		for _, svc := range sortedKeys(done.URLs) {
			fmt.Printf("%s answers at %s\n", svc, done.URLs[svc])
		}
	}
	return nil
}

// ---- diff and update ---------------------------------------------------

type diffResp struct {
	From string `json:"from"`
	To   string `json:"to"`
	Ops  []struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Detail string `json:"detail"`
	} `json:"ops"`
	Skipped   []string `json:"skipped"`
	Conflicts []struct {
		Path   string          `json:"path"`
		Base   json.RawMessage `json:"base"`
		Yours  json.RawMessage `json:"yours"`
		Theirs json.RawMessage `json:"theirs"`
	} `json:"conflicts"`
	Manual []struct {
		Path string `json:"path"`
		Why  string `json:"why"`
		How  string `json:"how"`
	} `json:"manual"`
	Removals       []string      `json:"removals"`
	InputsRequired []recipeInput `json:"inputs_required"`
	Monthly        struct {
		Before recipeMoney `json:"before"`
		After  recipeMoney `json:"after"`
	} `json:"monthly_micro_usd"`
}

func fetchDiff(project, to string) (diffResp, error) {
	body := map[string]any{}
	if to != "" {
		body["version"] = to
	}
	var d diffResp
	err := call("POST", "/v1/projects/"+project+"/recipe/diff", body, &d)
	return d, err
}

// plainJSON renders one value of a conflict: a string as itself, anything else
// as the JSON it arrived as. The platform has already redacted env values.
func plainJSON(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if len(raw) == 0 {
		return "nothing"
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

func cmdRecipeDiff(ref, to string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	if to != "" {
		if err := checkVersion(to); err != nil {
			return err
		}
	}
	d, err := fetchDiff(project, to)
	if err != nil {
		return err
	}
	printDiff(project, d)
	return nil
}

func printDiff(project string, d diffResp) {
	fmt.Printf("%s: %s → %s\n", project, d.From, d.To)
	if len(d.Ops) == 0 && len(d.Conflicts) == 0 && len(d.Manual) == 0 && len(d.Removals) == 0 {
		fmt.Println("  nothing the update would change")
	}
	if len(d.Ops) > 0 {
		fmt.Printf("\nchanges\n")
		for _, op := range d.Ops {
			fmt.Printf("  %-8s %s  %s\n", op.Kind, op.Name, op.Detail)
		}
	}
	if len(d.Skipped) > 0 {
		fmt.Printf("\nkept as you changed them\n")
		for _, s := range d.Skipped {
			fmt.Printf("  %s\n", s)
		}
	}
	if len(d.Conflicts) > 0 {
		fmt.Printf("\nconflicts: you and the recipe both changed these; update refuses them, --force takes the recipe's\n")
		for _, c := range d.Conflicts {
			fmt.Printf("  %s  was %s, yours %s, the recipe's %s\n",
				c.Path, plainJSON(c.Base), plainJSON(c.Yours), plainJSON(c.Theirs))
		}
	}
	if len(d.Manual) > 0 {
		fmt.Printf("\nby hand: an update cannot do these, and refuses until they are done\n")
		for _, m := range d.Manual {
			fmt.Printf("  %s  %s\n", m.Path, m.Why)
			if m.How != "" {
				fmt.Printf("    %s\n", m.How)
			}
		}
	}
	if len(d.Removals) > 0 {
		fmt.Printf("\nremoved, which needs a human's approval\n")
		for _, r := range d.Removals {
			fmt.Printf("  %s\n", r)
		}
	}
	if len(d.InputsRequired) > 0 {
		fmt.Printf("\nnew inputs, given to update as --input NAME=FILE\n")
		for _, in := range d.InputsRequired {
			fmt.Printf("  %s  %s\n", in.Name, strings.Join(in.Vars, ", "))
		}
	}
	fmt.Printf("\nabout %s now, %s after\n", d.Monthly.Before.line(), d.Monthly.After.line())
}

type updateResp struct {
	From    string            `json:"from"`
	To      string            `json:"to"`
	Applied []json.RawMessage `json:"applied"`
	Skipped []json.RawMessage `json:"skipped"`
}

// opText renders one entry of what an update applied or skipped: a sentence
// as itself, an op as its kind, name and detail.
func opText(raw json.RawMessage) string {
	var op struct{ Kind, Name, Detail string }
	if json.Unmarshal(raw, &op) == nil && op.Kind != "" {
		return strings.TrimSpace(op.Kind + " " + op.Name + "  " + op.Detail)
	}
	return plainJSON(raw)
}

// cmdRecipeUpdate moves an installed project to a newer version of its recipe.
//
// It asks for the diff first, for two reasons: the inputs a new version
// declares are refused here if there is no file for them, before anything is
// copied; and a removal is said before the gated call, the way `gg deps rm`
// says one. The update is then pinned to the version the diff showed, so what
// was read is what is applied.
//
// Then the same two steps install has, in finishWithInputs. Ask; if the
// platform has images to copy it answers 202, and gg waits for the copy and
// asks again with the inputs. If it had nothing to copy it applies at once —
// unless the version needs an input, which it refuses as inputs_missing, and
// that is the moment the files are read and sent.
func cmdRecipeUpdate(ref, to string, inputFlags []string, force bool) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	if to != "" {
		if err := checkVersion(to); err != nil {
			return err
		}
	}
	given, err := parseInputFlags(inputFlags)
	if err != nil {
		return err
	}
	d, err := fetchDiff(project, to)
	if err != nil {
		return err
	}
	if err := checkInputs("the update to "+d.To, d.InputsRequired, given); err != nil {
		return err
	}
	if err := statInputs(given); err != nil {
		return err
	}
	if len(d.Removals) > 0 {
		fmt.Printf("Updating %s to %s removes %s.\n", project, d.To, strings.Join(d.Removals, ", "))
		fmt.Printf("\n  What they ran stops, and whatever still calls them breaks with it.\n")
		printApprovalNote()
	}
	fmt.Printf("→ updating %s from %s to %s\n", project, d.From, d.To)

	body := map[string]any{}
	if d.To != "" {
		body["version"] = d.To
	}
	if force {
		body["force"] = true
	}
	out, err := finishWithInputs[updateResp](project, "/v1/projects/"+project+"/recipe/update", body, given, false)
	if err != nil {
		return err
	}

	fmt.Printf("%s is on %s, from %s\n", project, out.To, out.From)
	if len(out.Applied) > 0 {
		fmt.Printf("\napplied\n")
		for _, a := range out.Applied {
			fmt.Printf("  %s\n", opText(a))
		}
	}
	if len(out.Skipped) > 0 {
		fmt.Printf("\nkept as you changed them\n")
		for _, s := range out.Skipped {
			fmt.Printf("  %s\n", opText(s))
		}
	}
	fmt.Printf("\n`gg status %s` shows it rolling out.\n", project)
	return nil
}
