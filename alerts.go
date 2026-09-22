package main

// Where a project's alerts go: one ntfy topic.
//
// Turning alerts on is the whole of the setup. There are no rules to pick —
// the engine sends a fixed set to every project that names a topic: a service
// that is down, a deploy that will not start, a container that crashed and came
// back. So `gg alerts on shop` with nothing else is the common case, and it
// means ntfy.sh with a topic nobody can guess; the flags are for somebody with
// a server or a topic of their own.
//
// The token is never printed back. The engine does not return it, and this
// does not remember it.

import (
	"fmt"
)

type alertsView struct {
	Enabled   bool   `json:"enabled"`
	Server    string `json:"server"`
	Topic     string `json:"topic"`
	Subscribe string `json:"subscribe"`
	HasToken  bool   `json:"has_token"`
	UpdatedBy string `json:"updated_by"`
}

func cmdAlertsShow(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	var out alertsView
	if err := call("GET", "/v1/projects/"+project+"/alerts", nil, &out); err != nil {
		return err
	}
	if !out.Enabled {
		fmt.Printf("alerts are off for %s\n", project)
		fmt.Printf("\n  gg alerts on %s     to be told when a service goes down\n", project)
		return nil
	}
	printAlerts(project, out)
	return nil
}

func cmdAlertsOn(ref, server, topic, token string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	// Only what was said. No topic means "keep mine, or make one up", and no
	// server means ntfy.sh — both are the engine's to decide, not ours.
	body := map[string]string{}
	if server != "" {
		body["server"] = server
	}
	if topic != "" {
		body["topic"] = topic
	}
	if token != "" {
		body["token"] = token
	}
	var out alertsView
	if err := call("PUT", "/v1/projects/"+project+"/alerts", body, &out); err != nil {
		return err
	}
	printAlerts(project, out)
	fmt.Printf("\n  gg alerts test %s     once you have subscribed, to see one arrive\n", project)
	return nil
}

func printAlerts(project string, a alertsView) {
	fmt.Printf("alerts for %s go to %s\n\n", project, a.Subscribe)
	// The two fields the ntfy app asks for, in its words. Pasting the whole
	// address works on some platforms and not others; these always do.
	fmt.Printf("  in the ntfy app: subscribe to topic %s", a.Topic)
	if a.Server != "https://ntfy.sh" {
		fmt.Printf(" on server %s", a.Server)
	}
	fmt.Println()
	if a.HasToken {
		fmt.Println("  published with the token you gave")
	}
	fmt.Println("\nYou will be told when a service is down, a deploy will not start,")
	fmt.Println("or a container crashes and restarts.")
}

func cmdAlertsTest(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	var out struct {
		Subscribe string `json:"subscribe"`
	}
	if err := call("POST", "/v1/projects/"+project+"/alerts/test", nil, &out); err != nil {
		return err
	}
	fmt.Printf("sent a test to %s\n", out.Subscribe)
	fmt.Println("If nothing arrived, check the topic in the app matches that one.")
	return nil
}

func cmdAlertsOff(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	if err := call("DELETE", "/v1/projects/"+project+"/alerts", nil, nil); err != nil {
		return err
	}
	fmt.Printf("alerts are off for %s\n", project)
	return nil
}
