package main

// `gg recipe`, at the wire and in the terminal.
//
// The property worth most here is the one a happy-path test cannot see: an
// input file holds somebody's live keys, and gg must send them in exactly one
// request — the one after the copy — and print them nowhere, on any path,
// including the ones where something went wrong.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// reply is one canned answer.
type reply struct {
	status int
	body   string
}

// apiHit is one request the fake saw.
type apiHit struct {
	method, path string
	body         map[string]any
	raw          string
}

func (h apiHit) String() string { return h.method + " " + h.path }

// scriptedAPI answers each "METHOD /path" with the next reply queued for it,
// repeating the last, and records every request in order. A request with no
// route fails the test: an unexpected call is exactly what these tests are
// looking for.
func scriptedAPI(t *testing.T, routes map[string][]reply) *[]apiHit {
	t.Helper()
	recipePoll = time.Millisecond
	t.Cleanup(func() { recipePoll = 3 * time.Second })
	var mu sync.Mutex
	var hits []apiHit
	seen := map[string]int{}
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		raw := readBody(r)
		var body map[string]any
		_ = json.Unmarshal([]byte(raw), &body)
		key := r.Method + " " + r.URL.Path
		mu.Lock()
		hits = append(hits, apiHit{method: r.Method, path: r.URL.Path, body: body, raw: raw})
		n := seen[key]
		seen[key]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		rs, ok := routes[key]
		if !ok {
			t.Errorf("unexpected request %s %s", key, raw)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no route in the test"}}`))
			return
		}
		rp := rs[min(n, len(rs)-1)]
		w.WriteHeader(rp.status)
		_, _ = w.Write([]byte(rp.body))
	})
	return &hits
}

func ok(body string) []reply { return []reply{{http.StatusOK, body}} }

func paths(hits []apiHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.String()
	}
	return out
}

func hitsTo(hits []apiHit, key string) []apiHit {
	var out []apiHit
	for _, h := range hits {
		if h.String() == key {
			out = append(out, h)
		}
	}
	return out
}

// runCaptured runs f with stdout captured, and returns both.
func runCaptured(t *testing.T, f func() error) (string, error) {
	t.Helper()
	var err error
	out := capture(t, func() { err = f() })
	return out, err
}

// The id starts with a digit, so it cannot be a name and gg asks for it
// directly rather than looking through the caller's own recipes first.
const testRecipeID = "4bcd2fgh"

const testRecipe = `{"id":"4bcd2fgh","name":"chit-chat","official":false,
"publisher":{"github":"octocat"},"source_url":"https://github.com/octocat/chit-chat",
"latest":"1.3.0","version":"1.2.0","state":"published","notes":"first cut",
"versions":[{"version":"1.2.0","state":"published","published_at":"2026-11-01T10:00:00Z"},
            {"version":"1.3.0","state":"published","published_at":"2026-11-05T10:00:00Z"}],
"manifest":{"schema":1,
  "images":[{"repo":"web","digest":"sha256:aaaa"},{"repo":"api","digest":"sha256:bbbb"}],
  "services":[{"name":"web","repo":"web","port":8080,"size":"s","public":true,"deps":["api"]},
              {"name":"api","repo":"api","port":9000,"size":"m","deps":["db","openai"],
               "volume_path":"/data","volume_size_gb":5}],
  "jobs":[{"name":"nightly","repo":"api","schedule":"0 3 * * *","time_zone":"Europe/Berlin","size":"s","deps":["db"]}],
  "resources":[{"name":"db","type":"postgres","size":"m","storage_gb":10,"extensions":["vector"]},
               {"name":"openai","type":"external","size":""}],
  "inputs":[{"name":"openai","vars":["API_KEY","BASE_URL"]}]},
"inputs":[{"name":"openai","vars":["API_KEY","BASE_URL"]}],
"monthly_micro_usd":{"total":12500000,"running":10000000,"storage":2500000,"notes":["storage grows with the database"]}}`

// --- snap ------------------------------------------------------------------

func TestSnapSendsTheBodyAndPrintsTheURL(t *testing.T) {
	hits := scriptedAPI(t, map[string][]reply{
		"POST /v1/projects/shop/snap": {{http.StatusAccepted, `{
			"recipe":{"id":"k3f9x2ma","name":"shop-starter","exists":false},
			"previous":"","floor":"1.0.0","version":"1.0.0","changes":[],
			"notes":["shop/web's domain is not part of a recipe"],
			"manifest":{"schema":1},
			"monthly_micro_usd":{"total":5000000,"running":5000000,"storage":0},
			"state":"copying","next":"gg recipe ls shows when the copy is done",
			"deploy_url":"https://my.gagarin.cloud/deploy/k3f9x2ma"}`}},
	})
	out, err := runCaptured(t, func() error {
		return cmdRecipeSnap("shop", snapFlags{version: "1.0.0", as: "shop-starter",
			source: "https://github.com/octocat/shop", notes: "first cut"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(*hits) != 1 {
		t.Fatalf("requests = %v", paths(*hits))
	}
	body := (*hits)[0].body
	want := map[string]any{"name": "shop-starter", "version": "1.0.0",
		"source_url": "https://github.com/octocat/shop", "notes": "first cut"}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %#v, want %#v", body, want)
	}
	if _, has := body["dry_run"]; has {
		t.Errorf("a real snap sent dry_run: %#v", body)
	}
	for _, s := range []string{"shop-starter 1.0.0, its first version", "https://my.gagarin.cloud/deploy/k3f9x2ma",
		"[![Deploy to Gagarin Cloud](https://my.gagarin.cloud/deploy/k3f9x2ma/badge.svg)](https://my.gagarin.cloud/deploy/k3f9x2ma)",
		"copying its images", "gg recipe ls shows when", "domain is not part of a recipe", "$5/month"} {
		if !strings.Contains(out, s) {
			t.Errorf("%q missing from the output:\n%s", s, out)
		}
	}
}

// Nothing given is nothing sent: the platform's defaults (the project's name,
// the floor) apply only when the field is absent.
func TestSnapSendsOnlyWhatWasGiven(t *testing.T) {
	hits := scriptedAPI(t, map[string][]reply{
		"POST /v1/projects/shop/snap": {{http.StatusAccepted, `{"recipe":{"id":"k3f9x2ma","name":"shop"},"version":"1.0.0"}`}},
	})
	out, err := runCaptured(t, func() error { return cmdRecipeSnap("shop", snapFlags{}) })
	if err != nil {
		t.Fatal(err)
	}
	if b := (*hits)[0].raw; b != "{}" {
		t.Errorf("body = %s, want {}", b)
	}
	// No deploy_url in the answer: the link is built from the id.
	if !strings.Contains(out, deployLink("k3f9x2ma")) {
		t.Errorf("no deploy link:\n%s", out)
	}
}

func TestSnapDryRunMintsNothing(t *testing.T) {
	hits := scriptedAPI(t, map[string][]reply{
		"POST /v1/projects/shop/snap": ok(`{
			"recipe":{"id":"k3f9x2ma","name":"shop-starter","exists":true},
			"previous":"1.0.0","floor":"1.1.0","version":"1.1.0",
			"changes":[{"level":"minor","path":"services.worker","what":"added"},
			           {"level":"patch","path":"services.web.image","what":"new digest"}],
			"notes":[],"manifest":{"schema":1},
			"monthly_micro_usd":{"total":7500000,"running":5000000,"storage":2500000}}`),
	})
	out, err := runCaptured(t, func() error {
		return cmdRecipeSnap("shop", snapFlags{dryRun: true})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := (*hits)[0].body["dry_run"]; got != true {
		t.Fatalf("dry_run = %#v; a dry run that is not marked as one publishes", got)
	}
	for _, s := range []string{"Dry run: nothing was published", "shop-starter 1.1.0, after 1.0.0", "floor 1.1.0",
		"minor  services.worker  added", "patch  services.web.image  new digest",
		"$7.50/month ($5 running, $2.50 storage)", "Without --dry-run this publishes shop-starter 1.1.0"} {
		if !strings.Contains(out, s) {
			t.Errorf("%q missing from the output:\n%s", s, out)
		}
	}
	if strings.Contains(out, "published shop-starter") || strings.Contains(out, "/deploy/") {
		t.Errorf("a dry run reads as a publish:\n%s", out)
	}
}

func TestSnapRefusalPrintsCodeAndHint(t *testing.T) {
	for code, msg := range map[string]string{
		"env_looks_secret":    "services.api.env.STRIPE_KEY looks like a secret",
		"version_below_floor": "1.0.1 is below the floor 1.1.0: services.worker was added",
	} {
		t.Run(code, func(t *testing.T) {
			scriptedAPI(t, map[string][]reply{
				"POST /v1/projects/shop/snap": {{http.StatusBadRequest,
					`{"error":{"code":"` + code + `","message":"` + msg + `","fix_hint":"see gg recipe snap --help"}}`}},
			})
			out, err := runCaptured(t, func() error { return cmdRecipeSnap("shop", snapFlags{}) })
			if err == nil {
				t.Fatal("a refused snap returned no error")
			}
			want := "[" + code + "] " + msg + "\n  hint: see gg recipe snap --help"
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err, want)
			}
			if strings.Contains(out, "published") {
				t.Errorf("a refusal printed as a publish:\n%s", out)
			}
		})
	}
}

// --- show, ls, rm ----------------------------------------------------------

func TestShowPrintsTheGraphAndInputs(t *testing.T) {
	scriptedAPI(t, map[string][]reply{"GET /v1/recipes/" + testRecipeID: ok(testRecipe)})
	out, err := runCaptured(t, func() error { return cmdRecipeShow(testRecipeID, "text") })
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"chit-chat 1.2.0  (id 4bcd2fgh)",
		"published by github.com/octocat",
		"the latest is 1.3.0",
		"$12.50/month ($10 running, $2.50 storage)",
		"storage grows with the database",
		"  web      :8080  size s  public  → api\n",
		"  api      :9000  size m  volume /data 5 GB  → db, openai\n",
		`  nightly  "0 3 * * *" Europe/Berlin  size s  → db` + "\n",
		"  db       postgres  size m  10 GB  vector\n",
		"  openai   external\n",
		"inputs: an env file each, given on install as --input NAME=FILE\n  openai   API_KEY, BASE_URL\n",
		"deploy  https://my.gagarin.cloud/deploy/4bcd2fgh",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("%q missing from the output:\n%s", s, out)
		}
	}
	// No email, ever: the answer carries none, and nothing here may invent one.
	if strings.Contains(out, "@") {
		t.Errorf("an @ in the output, which is how an email would look:\n%s", out)
	}
}

func TestShowOfficialAndAVersion(t *testing.T) {
	official := strings.Replace(testRecipe, `"official":false`, `"official":true`, 1)
	hits := scriptedAPI(t, map[string][]reply{"GET /v1/recipes/" + testRecipeID + "/versions/1.2.0": ok(official)})
	out, err := runCaptured(t, func() error { return cmdRecipeShow(testRecipeID+"@1.2.0", "") })
	if err != nil {
		t.Fatal(err)
	}
	if len(*hits) != 1 {
		t.Errorf("requests = %v", paths(*hits))
	}
	if !strings.Contains(out, "official: published by Gagarin") {
		t.Errorf("no official badge:\n%s", out)
	}
}

func TestShowJSON(t *testing.T) {
	scriptedAPI(t, map[string][]reply{"GET /v1/recipes/" + testRecipeID: ok(testRecipe)})
	out, err := runCaptured(t, func() error { return cmdRecipeShow(testRecipeID, "json") })
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	_ = json.Unmarshal([]byte(testRecipe), &want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("--format json is not the platform's answer as sent:\n%s", out)
	}
}

func TestShowRefusesAnUnknownFormatBeforeAsking(t *testing.T) {
	hits := scriptedAPI(t, map[string][]reply{})
	if err := cmdRecipeShow(testRecipeID, "yaml"); err == nil || !strings.Contains(err.Error(), "text or json") {
		t.Errorf("err = %v", err)
	}
	if len(*hits) != 0 {
		t.Errorf("asked the platform anyway: %v", paths(*hits))
	}
}

// A name is one of your own, looked up in your list; something id-shaped that
// is not one of your names is somebody else's id.
func TestShowResolvesANameAmongYourRecipes(t *testing.T) {
	hits := scriptedAPI(t, map[string][]reply{
		"GET /v1/recipes":                 ok(`{"recipes":[{"id":"4bcd2fgh","name":"chit-chat","latest":"1.3.0","versions_count":2}]}`),
		"GET /v1/recipes/" + testRecipeID: ok(testRecipe),
		"GET /v1/recipes/abcdefgh":        ok(testRecipe),
	})
	if _, err := runCaptured(t, func() error { return cmdRecipeShow("chit-chat", "") }); err != nil {
		t.Fatal(err)
	}
	if _, err := runCaptured(t, func() error { return cmdRecipeShow("abcdefgh", "") }); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /v1/recipes", "GET /v1/recipes/4bcd2fgh", "GET /v1/recipes", "GET /v1/recipes/abcdefgh"}
	if got := paths(*hits); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
	err := cmdRecipeShow("not-mine", "")
	if err == nil || !strings.Contains(err.Error(), "you have no recipe called not-mine") || !strings.Contains(err.Error(), "hint:") {
		t.Errorf("err = %v", err)
	}
}

func TestLsEmptyAndFull(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		scriptedAPI(t, map[string][]reply{"GET /v1/recipes": ok(`{"recipes":[]}`)})
		out, err := runCaptured(t, cmdRecipeList)
		if err != nil {
			t.Fatal(err)
		}
		if out != "no recipes yet; gg recipe snap PROJECT publishes one\n" {
			t.Errorf("out = %q", out)
		}
	})
	t.Run("full", func(t *testing.T) {
		scriptedAPI(t, map[string][]reply{"GET /v1/recipes": ok(`{"recipes":[
			{"id":"4bcd2fgh","name":"chit-chat","latest":"1.3.0","versions_count":2,"copying":false},
			{"id":"k3f9x2ma","name":"shop-starter","latest":"1.0.0","versions_count":1,"copying":true}]}`)})
		out, err := runCaptured(t, cmdRecipeList)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 2 {
			t.Fatalf("lines = %q", lines)
		}
		for _, s := range []string{"chit-chat", "4bcd2fgh", "1.3.0", "2 versions"} {
			if !strings.Contains(lines[0], s) {
				t.Errorf("%q missing from %q", s, lines[0])
			}
		}
		if strings.Contains(lines[0], "copying") {
			t.Errorf("a settled recipe reads as copying: %q", lines[0])
		}
		if !strings.Contains(lines[1], "1 version") || !strings.HasSuffix(lines[1], "copying") {
			t.Errorf("line = %q", lines[1])
		}
	})
}

func TestRmWarnsThenCalls(t *testing.T) {
	approval := reply{http.StatusForbidden,
		`{"error":{"code":"approval_required","message":"we emailed the account owner","fix_hint":"run it again once they click"}}`}
	hits := scriptedAPI(t, map[string][]reply{
		"GET /v1/recipes": ok(`{"recipes":[{"id":"4bcd2fgh","name":"chit-chat","latest":"1.3.0","versions_count":2}]}`),
		"DELETE /v1/recipes/4bcd2fgh/versions/1.2.0": {approval, {http.StatusOK, `{"version":"1.2.0","state":"withdrawn"}`}},
		"DELETE /v1/recipes/4bcd2fgh":                {{http.StatusOK, `{"recipe":{"id":"4bcd2fgh","name":"chit-chat"}}`}},
	})

	out, err := runCaptured(t, func() error { return cmdRecipeRemove("chit-chat@1.2.0") })
	if err == nil || !strings.HasPrefix(err.Error(), "[approval_required]") {
		t.Fatalf("err = %v; approval_required has to reach the caller as itself", err)
	}
	for _, s := range []string{"Withdrawing chit-chat 1.2.0", "This needs a human", "run the same"} {
		if !strings.Contains(out, s) {
			t.Errorf("%q missing from the warning:\n%s", s, out)
		}
	}
	if strings.Contains(out, "withdrawn") {
		t.Errorf("a gated call printed as done:\n%s", out)
	}
	if got := paths(*hits); !reflect.DeepEqual(got, []string{"GET /v1/recipes", "DELETE /v1/recipes/4bcd2fgh/versions/1.2.0"}) {
		t.Errorf("requests = %v", got)
	}

	// Once the owner has clicked, the same command goes through.
	out, err = runCaptured(t, func() error { return cmdRecipeRemove("chit-chat@1.2.0") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chit-chat 1.2.0 withdrawn") {
		t.Errorf("out:\n%s", out)
	}

	// No version is the whole recipe, and it says so.
	out, err = runCaptured(t, func() error { return cmdRecipeRemove("chit-chat") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "every version") || !strings.Contains(out, "This needs a human") {
		t.Errorf("out:\n%s", out)
	}
	if len(hitsTo(*hits, "DELETE /v1/recipes/4bcd2fgh")) != 1 {
		t.Errorf("requests = %v", paths(*hits))
	}
}

// A name that is not one of yours is answered, not warned about and sent.
func TestRmOfANameYouDoNotHaveSendsNothing(t *testing.T) {
	hits := scriptedAPI(t, map[string][]reply{"GET /v1/recipes": ok(`{"recipes":[]}`)})
	out, err := runCaptured(t, func() error { return cmdRecipeRemove("chit-chat") })
	if err == nil || !strings.Contains(err.Error(), "no recipe called chit-chat") {
		t.Fatalf("err = %v", err)
	}
	if out != "" {
		t.Errorf("warned about something that was never going to happen:\n%s", out)
	}
	if len(*hits) != 1 {
		t.Errorf("requests = %v", paths(*hits))
	}
}

// --- install ---------------------------------------------------------------

const testSecret = "sk-live-SECRET123"

// installRoutes is the whole happy path, with the copy taking two polls.
func installRoutes() map[string][]reply {
	return map[string][]reply{
		"GET /v1/recipes/" + testRecipeID: ok(testRecipe),
		"POST /v1/projects": {{http.StatusCreated,
			`{"id":"p9","name":"myshop","role":"owner","cluster":"fr-par-1"}`}},
		"POST /v1/projects/myshop/recipe": {
			{http.StatusAccepted, `{"project":"myshop","project_id":"p9",
				"recipe":{"id":"4bcd2fgh","name":"chit-chat","version":"1.2.0"},
				"install":{"state":"copying","attempts":0},
				"inputs_required":[{"name":"openai","vars":["API_KEY","BASE_URL"]}],
				"monthly_micro_usd":{"total":12500000},"next":"poll GET"}`},
			{http.StatusOK, `{"install":{"state":"installed","version":"1.2.0"},
				"urls":{"web":"https://web-p9.fr-par-1.gagarin.run"}}`},
		},
		"GET /v1/projects/myshop/recipe": {
			{http.StatusOK, `{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copying"}`},
			{http.StatusOK, `{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copied"}`},
		},
		"GET /v1/projects/myshop/status": ok(`{"project":"myshop","project_id":"p9","services":[]}`),
	}
}

func openaiEnv(t *testing.T) string {
	return writeTemp(t, ".env.openai", "API_KEY="+testSecret+"\nBASE_URL=https://api.openai.com/v1\n")
}

func TestInstallSendsNoInputsUntilCopied(t *testing.T) {
	hits := scriptedAPI(t, installRoutes())
	file := openaiEnv(t)
	out, err := runCaptured(t, func() error {
		return cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file})
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /v1/recipes/4bcd2fgh",
		"POST /v1/projects",
		"POST /v1/projects/myshop/recipe",
		"GET /v1/projects/myshop/recipe",
		"GET /v1/projects/myshop/recipe",
		"POST /v1/projects/myshop/recipe",
		"GET /v1/projects/myshop/status",
	}
	if got := paths(*hits); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests =\n  %v\nwant\n  %v", got, want)
	}
	if got := (*hits)[1].body; !reflect.DeepEqual(got, map[string]any{"name": "myshop"}) {
		t.Errorf("project body = %#v", got)
	}
	first := (*hits)[2].body
	if _, has := first["inputs"]; has {
		t.Errorf("the first POST carried inputs, before anything was copied: %s", (*hits)[2].raw)
	}
	if !reflect.DeepEqual(first, map[string]any{"recipe": testRecipeID, "version": "1.2.0"}) {
		t.Errorf("first body = %#v", first)
	}
	final := (*hits)[5].body
	wantInputs := map[string]any{"openai": map[string]any{"API_KEY": testSecret, "BASE_URL": "https://api.openai.com/v1"}}
	if !reflect.DeepEqual(final["inputs"], wantInputs) {
		t.Errorf("final inputs = %#v, want exactly the file's pairs", final["inputs"])
	}
	if final["recipe"] != testRecipeID || final["version"] != "1.2.0" {
		t.Errorf("final body = %#v", final)
	}
	if n := strings.Count(out, "copying images…"); n != 1 {
		t.Errorf("copying line printed %d times:\n%s", n, out)
	}
	for _, s := range []string{"project myshop created (id p9)", "installed chit-chat 1.2.0 into myshop",
		"project myshop  (id p9)", "web answers at https://web-p9.fr-par-1.gagarin.run"} {
		if !strings.Contains(out, s) {
			t.Errorf("%q missing from the output:\n%s", s, out)
		}
	}
}

// SAFETY. An input value appears in exactly one request body and in no output,
// on every path: success, a failed copy, a refusal from the platform, and a
// file gg cannot parse. The last is the one that fails if parseEnvFile ever
// goes back to quoting the line it could not read.
func TestInstallNeverPrintsAnInputValue(t *testing.T) {
	cases := map[string]struct {
		file    string
		routes  func(map[string][]reply)
		wantErr string
	}{
		"success": {routes: func(map[string][]reply) {}},
		"the copy failed": {
			routes: func(r map[string][]reply) {
				r["GET /v1/projects/myshop/recipe"] = ok(`{"recipe":{"id":"4bcd2fgh"},"op":"install","state":"failed","error":"registry refused the copy"}`)
			},
			wantErr: "registry refused the copy",
		},
		"inputs_missing": {
			routes: func(r map[string][]reply) {
				r["POST /v1/projects/myshop/recipe"] = []reply{r["POST /v1/projects/myshop/recipe"][0],
					{http.StatusBadRequest, `{"error":{"code":"inputs_missing","message":"openai needs API_KEY","fix_hint":"--input openai=FILE"}}`}}
			},
			wantErr: "[inputs_missing]",
		},
		"invalid_env": {
			routes: func(r map[string][]reply) {
				r["POST /v1/projects/myshop/recipe"] = []reply{r["POST /v1/projects/myshop/recipe"][0],
					{http.StatusBadRequest, `{"error":{"code":"invalid_env","message":"openai: BASE_URL is not a URL"}}`}}
			},
			wantErr: "[invalid_env]",
		},
		"a line gg cannot read": {
			// The key pasted on a line of its own, without its name.
			file:    "BASE_URL=https://api.openai.com/v1\n" + testSecret + "\n",
			wantErr: "line 2",
		},
		"a line whose name is not a name": {
			file:    testSecret + "=x\n",
			wantErr: "line 1",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			routes := installRoutes()
			if tc.routes != nil {
				tc.routes(routes)
			}
			hits := scriptedAPI(t, routes)
			file := openaiEnv(t)
			if tc.file != "" {
				file = writeTemp(t, ".env.openai", tc.file)
			}
			out, err := runCaptured(t, func() error {
				return cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file})
			})
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want it to say %q", err, tc.wantErr)
			}
			if strings.Contains(out, testSecret) {
				t.Errorf("an input value reached stdout:\n%s", out)
			}
			if err != nil && strings.Contains(err.Error(), testSecret) {
				t.Errorf("an input value reached the error: %v", err)
			}
			carrying := 0
			for _, h := range *hits {
				if strings.Contains(h.raw, testSecret) {
					carrying++
					if _, has := h.body["inputs"]; !has || h.method != "POST" || h.path != "/v1/projects/myshop/recipe" {
						t.Errorf("an input value went out in %s: %s", h, h.raw)
					}
				}
			}
			if carrying > 1 {
				t.Errorf("input values went out in %d requests, want at most one", carrying)
			}
		})
	}
}

func TestInstallRefusesAMissingInputBeforeCreatingAProject(t *testing.T) {
	for name, tc := range map[string]struct {
		inputs []string
		want   []string
	}{
		"missing":    {nil, []string{"chit-chat", "has none for openai", "--input openai=FILE (API_KEY, BASE_URL)"}},
		"undeclared": {[]string{"openai=ENV", "stripe=ENV"}, []string{"chit-chat has no input called stripe", "it takes openai"}},
		"no file":    {[]string{"openai=/nonexistent/.env.openai"}, []string{"--input openai: there is no file"}},
		"no equals":  {[]string{testSecret}, []string{"--input expects NAME=FILE"}},
	} {
		t.Run(name, func(t *testing.T) {
			hits := scriptedAPI(t, map[string][]reply{"GET /v1/recipes/" + testRecipeID: ok(testRecipe)})
			env := openaiEnv(t)
			var inputs []string
			for _, in := range tc.inputs {
				inputs = append(inputs, strings.Replace(in, "ENV", env, 1))
			}
			out, err := runCaptured(t, func() error { return cmdRecipeInstall(testRecipeID, "myshop", inputs) })
			if err == nil {
				t.Fatal("installed with a bad --input")
			}
			for _, s := range tc.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("%q missing from %q", s, err)
				}
			}
			if strings.Contains(err.Error(), testSecret) || strings.Contains(out, testSecret) {
				t.Errorf("what was typed after the name was said back: %v", err)
			}
			for _, h := range *hits {
				if h.method != "GET" {
					t.Errorf("%s was sent before the inputs were checked", h)
				}
			}
		})
	}
}

// Run again after a refused input, the command carries on in the project the
// first run created rather than refusing because the name is taken.
//
// The fake answers as the platform does: a project whose images are copied
// refuses a POST without inputs as inputs_missing, and never says "copied"
// to one. So the run that carries on must send the inputs in its first POST,
// and a run that finds the copy still going must ask, wait, and then send.
func TestInstallCarriesOnInTheProjectItCreated(t *testing.T) {
	nameTaken := []reply{{http.StatusConflict,
		`{"error":{"code":"name_taken","message":"you already have a project called myshop"}}`}}
	inputsMissing := reply{http.StatusBadRequest,
		`{"error":{"code":"inputs_missing","message":"this recipe needs values for openai"}}`}
	installed := reply{http.StatusOK, `{"install":{"state":"installed","version":"1.2.0"},"urls":{}}`}
	file := openaiEnv(t)

	t.Run("copied: the inputs go in the first POST", func(t *testing.T) {
		routes := installRoutes()
		routes["POST /v1/projects"] = nameTaken
		routes["GET /v1/projects/myshop/recipe"] = ok(`{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copied","version":"1.2.0"}`)
		routes["POST /v1/projects/myshop/recipe"] = []reply{inputsMissing}
		hits := scriptedAPI(t, routes)
		out, err := runCaptured(t, func() error {
			return cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file})
		})
		// The fake answers every POST without inputs as inputs_missing, so
		// the only way through is one POST that carries them.
		posts := hitsTo(*hits, "POST /v1/projects/myshop/recipe")
		if len(posts) != 1 || posts[0].body["inputs"] == nil {
			t.Fatalf("err = %v, posts = %v", err, posts)
		}
		if !strings.Contains(out, "carrying on") || strings.Contains(out, "copying images") {
			t.Errorf("out:\n%s", out)
		}
		// The fake's inputs_missing is the only reply queued, so the run ends
		// in it; that it carried the inputs, above, is the property.
		if err == nil || !strings.Contains(err.Error(), "[inputs_missing]") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("copied: the run finishes", func(t *testing.T) {
		routes := installRoutes()
		routes["POST /v1/projects"] = nameTaken
		routes["GET /v1/projects/myshop/recipe"] = ok(`{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copied","version":"1.2.0"}`)
		routes["POST /v1/projects/myshop/recipe"] = []reply{installed}
		hits := scriptedAPI(t, routes)
		out, err := runCaptured(t, func() error {
			return cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file})
		})
		if err != nil {
			t.Fatal(err)
		}
		posts := hitsTo(*hits, "POST /v1/projects/myshop/recipe")
		if len(posts) != 1 || posts[0].body["inputs"] == nil || posts[0].body["version"] != "1.2.0" {
			t.Errorf("posts = %v", posts)
		}
		if !strings.Contains(out, "carrying on") || !strings.Contains(out, "installed chit-chat 1.2.0 into myshop") {
			t.Errorf("out:\n%s", out)
		}
	})

	t.Run("still copying: ask, wait, then send", func(t *testing.T) {
		routes := installRoutes()
		routes["POST /v1/projects"] = nameTaken
		routes["GET /v1/projects/myshop/recipe"] = []reply{
			{http.StatusOK, `{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copying","version":"1.2.0"}`},
			{http.StatusOK, `{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copied","version":"1.2.0"}`},
		}
		routes["POST /v1/projects/myshop/recipe"] = []reply{routes["POST /v1/projects/myshop/recipe"][0], installed}
		hits := scriptedAPI(t, routes)
		out, err := runCaptured(t, func() error {
			return cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file})
		})
		if err != nil {
			t.Fatal(err)
		}
		posts := hitsTo(*hits, "POST /v1/projects/myshop/recipe")
		if len(posts) != 2 || posts[0].body["inputs"] != nil || posts[1].body["inputs"] == nil {
			t.Errorf("posts = %v", posts)
		}
		if !strings.Contains(out, "carrying on") || !strings.Contains(out, "copying images") {
			t.Errorf("out:\n%s", out)
		}
	})

	// The copy finished between the GET and the POST: the platform answers
	// the ask inputs_missing, which means "copied, send them".
	t.Run("copied meanwhile: inputs_missing on the ask is not the end", func(t *testing.T) {
		routes := installRoutes()
		routes["POST /v1/projects"] = nameTaken
		routes["GET /v1/projects/myshop/recipe"] = ok(`{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copying","version":"1.2.0"}`)
		routes["POST /v1/projects/myshop/recipe"] = []reply{inputsMissing, installed}
		hits := scriptedAPI(t, routes)
		out, err := runCaptured(t, func() error {
			return cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file})
		})
		if err != nil {
			t.Fatal(err)
		}
		posts := hitsTo(*hits, "POST /v1/projects/myshop/recipe")
		if len(posts) != 2 || posts[0].body["inputs"] != nil || posts[1].body["inputs"] == nil {
			t.Errorf("posts = %v", posts)
		}
		if strings.Contains(out, "copying images") {
			t.Errorf("out:\n%s", out)
		}
	})

	// A newer version was published between the runs. The install in flight
	// is 1.2.0, and that is what the run finishes, whatever is latest now.
	t.Run("pinned to the version in flight", func(t *testing.T) {
		routes := installRoutes()
		routes["POST /v1/projects"] = nameTaken
		routes["GET /v1/recipes/4bcd2fgh"] = ok(strings.Replace(testRecipe, `"version":"1.2.0"`, `"version":"1.3.0"`, 1))
		routes["GET /v1/recipes/4bcd2fgh/versions/1.2.0"] = ok(testRecipe)
		routes["GET /v1/projects/myshop/recipe"] = ok(`{"recipe":{"id":"4bcd2fgh","name":"chit-chat"},"op":"install","state":"copied","version":"1.2.0"}`)
		routes["POST /v1/projects/myshop/recipe"] = []reply{installed}
		hits := scriptedAPI(t, routes)
		out, err := runCaptured(t, func() error {
			return cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file})
		})
		if err != nil {
			t.Fatal(err)
		}
		posts := hitsTo(*hits, "POST /v1/projects/myshop/recipe")
		if len(posts) != 1 || posts[0].body["version"] != "1.2.0" {
			t.Errorf("posts = %v", posts)
		}
		if !strings.Contains(out, "carrying on") || !strings.Contains(out, "installing chit-chat 1.2.0; carrying on") {
			t.Errorf("out:\n%s", out)
		}
	})

	// A project of the same name that holds anything else is refused as the
	// platform refused it.
	t.Run("another project of that name", func(t *testing.T) {
		routes := installRoutes()
		routes["POST /v1/projects"] = nameTaken
		routes["GET /v1/projects/myshop/recipe"] = []reply{{http.StatusNotFound, `{"error":{"code":"no_recipe","message":"no recipe"}}`}}
		scriptedAPI(t, routes)
		if err := cmdRecipeInstall(testRecipeID, "myshop", []string{"openai=" + file}); err == nil || !strings.Contains(err.Error(), "[name_taken]") {
			t.Errorf("err = %v", err)
		}
	})
}

// A poll that does not reach the platform is retried; a poll the platform
// refuses is not. The fake drops the connection, which is what a network
// that blinked looks like from here.
func TestWaitForRecipeCopyToleratesABlinkingNetwork(t *testing.T) {
	recipePoll = time.Millisecond
	t.Cleanup(func() { recipePoll = 3 * time.Second })
	drop := func(w http.ResponseWriter) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			panic(err)
		}
		conn.Close()
	}
	for name, tc := range map[string]struct {
		drops   int
		refuse  bool
		wantErr string
	}{
		"two drops, then copied": {drops: 2},
		"every poll dropped":     {drops: 100, wantErr: "cannot reach control plane"},
		"refused at once":        {refuse: true, wantErr: "[forbidden]"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch {
				case tc.refuse:
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"no"}}`))
				case calls <= tc.drops:
					drop(w)
				default:
					_, _ = w.Write([]byte(`{"state":"copied"}`))
				}
			})
			err := waitForRecipeCopy("myshop")
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			switch {
			case tc.refuse && calls != 1:
				t.Errorf("a refusal was retried: %d calls", calls)
			case tc.drops == 100 && calls != copyPollFailures:
				t.Errorf("gave up after %d calls, want %d", calls, copyPollFailures)
			case tc.drops == 2 && calls != 3:
				t.Errorf("%d calls, want 3", calls)
			}
		})
	}
}

// Reading a recipe needs no account, so no credential file is forgiven; a
// credential file that cannot be read is not, because the alternative is to
// act as nobody without saying so.
func TestReadingARecipeSignedOut(t *testing.T) {
	t.Run("no credentials at all", func(t *testing.T) {
		hits := scriptedAPI(t, map[string][]reply{"GET /v1/recipes/" + testRecipeID: ok(testRecipe)})
		t.Setenv("GAGARIN_TOKEN", "")
		if _, _, err := fetchRecipe(testRecipeID, ""); err != nil {
			t.Fatal(err)
		}
		if len(*hits) != 1 {
			t.Errorf("requests = %v", paths(*hits))
		}
	})
	t.Run("a credential file that cannot be read", func(t *testing.T) {
		hits := scriptedAPI(t, map[string][]reply{"GET /v1/recipes/" + testRecipeID: ok(testRecipe)})
		t.Setenv("GAGARIN_TOKEN", "")
		dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "gagarin")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := fetchRecipe(testRecipeID, "")
		if err == nil || !strings.Contains(err.Error(), "not readable as credentials") {
			t.Fatalf("err = %v", err)
		}
		if len(*hits) != 0 {
			t.Errorf("a request went out as nobody: %v", paths(*hits))
		}
	})
}

// --- refs ------------------------------------------------------------------

func TestParseRecipeRef(t *testing.T) {
	for _, tc := range []struct {
		in, ref, version, err string
	}{
		{in: "chit-chat", ref: "chit-chat"},
		{in: "4bcd2fgh", ref: "4bcd2fgh"},
		{in: "abcdefgh", ref: "abcdefgh"},
		{in: "chit-chat@1.2.0", ref: "chit-chat", version: "1.2.0"},
		{in: "4bcd2fgh@10.0.12", ref: "4bcd2fgh", version: "10.0.12"},
		{in: "https://my.gagarin.cloud/deploy/4bcd2fgh", ref: "4bcd2fgh"},
		{in: "http://my.gagarin.cloud/deploy/4bcd2fgh", ref: "4bcd2fgh"},
		{in: "my.gagarin.cloud/deploy/4bcd2fgh", ref: "4bcd2fgh"},
		{in: "https://my.gagarin.cloud/deploy/4bcd2fgh/", ref: "4bcd2fgh"},
		{in: "https://my.gagarin.cloud/deploy/4bcd2fgh?utm_source=x", ref: "4bcd2fgh"},
		{in: "https://my.gagarin.cloud/deploy/4bcd2fgh#install", ref: "4bcd2fgh"},
		{in: "https://my.gagarin.cloud/deploy/4bcd2fgh/?ref=1#top", ref: "4bcd2fgh"},
		{in: "https://my.gagarin.cloud/deploy/4bcd2fgh@1.2.0", ref: "4bcd2fgh", version: "1.2.0"},
		{in: "https://my.gagarin.cloud/deploy/", err: "not a recipe name or id"},
		{in: "https://my.gagarin.cloud/projects/4bcd2fgh", err: "not inside a project"},
		{in: "", err: "which recipe?"},
		{in: "shop/web", err: "not inside a project"},
		{in: "Chit-Chat", err: "not a recipe name or id"},
		{in: "1234567", err: "not a recipe name or id"},
		{in: "chit-chat@", err: "not a version"},
		{in: "chit-chat@latest", err: "not a version"},
		{in: "chit-chat@v1.2.0", err: "not a version"},
		{in: "chit-chat@1.2", err: "not a version"},
		{in: "chit-chat@1.2.0.1", err: "not a version"},
	} {
		ref, version, err := parseRecipeRef(tc.in)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%q: err = %v, want %q", tc.in, err, tc.err)
			}
			continue
		}
		if err != nil || ref != tc.ref || version != tc.version {
			t.Errorf("%q = (%q, %q, %v), want (%q, %q)", tc.in, ref, version, err, tc.ref, tc.version)
		}
	}
}

// --- diff and update -------------------------------------------------------

// testDiff is the engine's answer, shape for shape: the plan under "plan",
// its new inputs as new_inputs, the bill either side beside it. A fixture
// that matched gg's reader rather than the engine once hid that they had
// drifted apart, and `gg recipe diff` printed an update that changed nothing.
const testDiff = `{"plan":{"from":"1.2.0","to":"1.3.0",
"ops":[{"kind":"deploy","name":"web","detail":"new image"},{"kind":"create","name":"stripe","detail":"input"}],
"skipped":["services.api.env.LOG_LEVEL"],
"conflicts":[{"path":"services.api.size","base":"s","yours":"m","theirs":"l"}],
"manual":[],
"removals":["worker"],
"new_inputs":[{"name":"stripe","vars":["API_KEY"]}]},
"needs_copy":true,
"monthly_micro_usd":{"before":{"total":12500000},"after":{"total":15000000}}}`

func TestDiffAndUpdate(t *testing.T) {
	t.Run("diff", func(t *testing.T) {
		hits := scriptedAPI(t, map[string][]reply{"POST /v1/projects/myshop/recipe/diff": ok(testDiff)})
		out, err := runCaptured(t, func() error { return cmdRecipeDiff("myshop", "1.3.0") })
		if err != nil {
			t.Fatal(err)
		}
		if got := (*hits)[0].body; !reflect.DeepEqual(got, map[string]any{"version": "1.3.0"}) {
			t.Errorf("body = %#v", got)
		}
		for _, s := range []string{"myshop: 1.2.0 → 1.3.0", "deploy    web  new image", "services.api.env.LOG_LEVEL",
			"services.api.size  was s, yours m, the recipe's l", "removed, which needs a human's approval\n  worker",
			"stripe  API_KEY", "$12.50/month now, $15/month after"} {
			if !strings.Contains(out, s) {
				t.Errorf("%q missing from the output:\n%s", s, out)
			}
		}
	})

	t.Run("copies, then applies with the inputs", func(t *testing.T) {
		hits := scriptedAPI(t, map[string][]reply{
			"POST /v1/projects/myshop/recipe/diff": ok(testDiff),
			"POST /v1/projects/myshop/recipe/update": {
				{http.StatusAccepted, `{"state":"copying"}`},
				{http.StatusOK, `{"from":"1.2.0","to":"1.3.0","applied":[{"kind":"deploy","name":"web","detail":"new image"}],"skipped":["services.api.env.LOG_LEVEL"]}`},
			},
			"GET /v1/projects/myshop/recipe": {
				{http.StatusOK, `{"op":"update","state":"copying"}`},
				{http.StatusOK, `{"op":"update","state":"copied"}`},
			},
		})
		file := writeTemp(t, ".env.stripe", "API_KEY="+testSecret+"\n")
		out, err := runCaptured(t, func() error {
			return cmdRecipeUpdate("myshop", "", []string{"stripe=" + file}, true)
		})
		if err != nil {
			t.Fatal(err)
		}
		updates := hitsTo(*hits, "POST /v1/projects/myshop/recipe/update")
		if len(updates) != 2 {
			t.Fatalf("requests = %v", paths(*hits))
		}
		// Pinned to the version the diff showed, so what was warned about is
		// what is applied.
		if !reflect.DeepEqual(updates[0].body, map[string]any{"version": "1.3.0", "force": true}) {
			t.Errorf("first update body = %s", updates[0].raw)
		}
		want := map[string]any{"version": "1.3.0", "force": true,
			"inputs": map[string]any{"stripe": map[string]any{"API_KEY": testSecret}}}
		if !reflect.DeepEqual(updates[1].body, want) {
			t.Errorf("second update body = %#v", updates[1].body)
		}
		warn := strings.Index(out, "removes worker")
		start := strings.Index(out, "→ updating myshop from 1.2.0 to 1.3.0")
		if warn < 0 || start < 0 || warn > start || !strings.Contains(out, "This needs a human") {
			t.Errorf("the removal was not said before the update was asked for:\n%s", out)
		}
		for _, s := range []string{"copying images…", "myshop is on 1.3.0, from 1.2.0", "deploy web  new image"} {
			if !strings.Contains(out, s) {
				t.Errorf("%q missing from the output:\n%s", s, out)
			}
		}
		if strings.Contains(out, testSecret) {
			t.Errorf("an input value reached stdout:\n%s", out)
		}
	})

	t.Run("applies at once, and sends inputs only when asked for them", func(t *testing.T) {
		hits := scriptedAPI(t, map[string][]reply{
			"POST /v1/projects/myshop/recipe/diff": ok(testDiff),
			"POST /v1/projects/myshop/recipe/update": {
				{http.StatusBadRequest, `{"error":{"code":"inputs_missing","message":"stripe"}}`},
				{http.StatusOK, `{"from":"1.2.0","to":"1.3.0","applied":["deployed web"]}`},
			},
		})
		file := writeTemp(t, ".env.stripe", "API_KEY="+testSecret+"\n")
		out, err := runCaptured(t, func() error {
			return cmdRecipeUpdate("myshop", "1.3.0", []string{"stripe=" + file}, false)
		})
		if err != nil {
			t.Fatal(err)
		}
		updates := hitsTo(*hits, "POST /v1/projects/myshop/recipe/update")
		if len(updates) != 2 || strings.Contains(updates[0].raw, testSecret) || !strings.Contains(updates[1].raw, testSecret) {
			t.Errorf("updates = %v", updates)
		}
		if strings.Contains(out, "copying images") || !strings.Contains(out, "deployed web") {
			t.Errorf("out:\n%s", out)
		}
	})

	// SAFETY. An update that removes something is gated on the account owner,
	// and the gate has to reach the caller as itself: not swallowed, not
	// printed over with a line that says the update went through. The
	// platform asks for the inputs before it asks for approval, so the
	// refusal comes in answer to the POST that carried them.
	t.Run("a removal's approval_required reaches the caller", func(t *testing.T) {
		approval := reply{http.StatusForbidden,
			`{"error":{"code":"approval_required","message":"we emailed the account owner","fix_hint":"run it again once they click"}}`}
		hits := scriptedAPI(t, map[string][]reply{
			"POST /v1/projects/myshop/recipe/diff": ok(testDiff),
			"POST /v1/projects/myshop/recipe/update": {
				{http.StatusBadRequest, `{"error":{"code":"inputs_missing","message":"stripe"}}`},
				approval,
			},
		})
		file := writeTemp(t, ".env.stripe", "API_KEY="+testSecret+"\n")
		out, err := runCaptured(t, func() error {
			return cmdRecipeUpdate("myshop", "", []string{"stripe=" + file}, false)
		})
		if err == nil || !strings.HasPrefix(err.Error(), "[approval_required]") {
			t.Fatalf("err = %v; approval_required has to reach the caller as itself", err)
		}
		for _, s := range []string{"removes worker", "This needs a human"} {
			if !strings.Contains(out, s) {
				t.Errorf("%q missing from the warning:\n%s", s, out)
			}
		}
		if strings.Contains(out, "myshop is on") || strings.Contains(out, "rolling out") {
			t.Errorf("a gated call printed as done:\n%s", out)
		}
		updates := hitsTo(*hits, "POST /v1/projects/myshop/recipe/update")
		if len(updates) != 2 || strings.Contains(updates[0].raw, testSecret) || !strings.Contains(updates[1].raw, testSecret) {
			t.Errorf("updates = %v", updates)
		}
		if strings.Contains(out, testSecret) || strings.Contains(err.Error(), testSecret) {
			t.Errorf("an input value was printed:\n%s\n%v", out, err)
		}

		// Once the owner has clicked, the same command goes through.
		scriptedAPI(t, map[string][]reply{
			"POST /v1/projects/myshop/recipe/diff": ok(testDiff),
			"POST /v1/projects/myshop/recipe/update": {
				{http.StatusBadRequest, `{"error":{"code":"inputs_missing","message":"stripe"}}`},
				{http.StatusOK, `{"from":"1.2.0","to":"1.3.0","applied":["removed worker"]}`},
			},
		})
		out, err = runCaptured(t, func() error {
			return cmdRecipeUpdate("myshop", "", []string{"stripe=" + file}, false)
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "myshop is on 1.3.0, from 1.2.0") || !strings.Contains(out, "removed worker") {
			t.Errorf("out:\n%s", out)
		}
	})

	t.Run("a new input with no file is refused before the update is asked for", func(t *testing.T) {
		hits := scriptedAPI(t, map[string][]reply{"POST /v1/projects/myshop/recipe/diff": ok(testDiff)})
		err := cmdRecipeUpdate("myshop", "", nil, false)
		if err == nil || !strings.Contains(err.Error(), "--input stripe=FILE") {
			t.Fatalf("err = %v", err)
		}
		if len(*hits) != 1 {
			t.Errorf("requests = %v", paths(*hits))
		}
	})
}
