package main

// Alerts are notifications from the console, not a topic somebody subscribes to.
//
// Turning them on is a per-member opt-in: it says "tell me about this project",
// and nothing else. Where they arrive is a browser the member has allowed
// notifications in, at my.gagarin.cloud — and a CLI cannot do that for them, so
// `gg alerts on` says so rather than pretending the job is done. A member with
// no device yet is told that first, because opting in alone delivers nothing.
//
// There is no server, topic or token any more. The engine refuses a body that
// carries one, so the command sends none.

import (
	"errors"
	"fmt"
	"net/url"
	"time"
)

type alertsView struct {
	Enabled bool `json:"enabled"`
	Devices int  `json:"devices"`
}

type notification struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Resolved  bool   `json:"resolved"`
	CreatedAt string `json:"created_at"`
}

// alertsPage is where a project's alerts are managed, and where a device is
// added — the one step that has to happen in a browser.
func alertsPage(project string) string {
	return "https://my.gagarin.cloud/projects/" + url.PathEscape(project) + "/alerts"
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
	var feed struct {
		Notifications []notification `json:"notifications"`
	}
	if err := call("GET", "/v1/projects/"+project+"/notifications?limit=5", nil, &feed); err != nil {
		return err
	}
	if !out.Enabled {
		fmt.Printf("alerts are off for %s\n", project)
		fmt.Printf("\n  gg alerts on %s     to be told when a service goes down\n", project)
	} else {
		fmt.Printf("alerts are on for %s\n", project)
		fmt.Printf("  %d device(s) receiving them\n", out.Devices)
		if out.Devices == 0 {
			printNoDevices(project)
		}
	}
	if len(feed.Notifications) == 0 {
		fmt.Println("\nno recent notifications")
		return nil
	}
	fmt.Println("\nrecent notifications")
	for _, n := range feed.Notifications {
		mark := ""
		if n.Resolved {
			mark = "  [resolved]"
		}
		fmt.Printf("  %s  %s%s\n", notificationTime(n.CreatedAt), n.Title, mark)
	}
	return nil
}

// notificationTime shows the member's own clock; a value that does not parse is
// printed as it came rather than hidden.
func notificationTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04")
}

func printNoDevices(project string) {
	fmt.Println("\n  NO DEVICE YET: nothing can reach you until you add one.")
	fmt.Printf("  Open %s in a browser and allow notifications.\n", alertsPage(project))
}

func cmdAlertsOn(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	// No body: opting in carries nothing, and a body naming a server, topic or
	// token is refused by the engine.
	var out alertsView
	if err := call("PUT", "/v1/projects/"+project+"/alerts", nil, &out); err != nil {
		return err
	}
	fmt.Printf("alerts are on for %s\n", project)
	if out.Devices == 0 {
		printNoDevices(project)
	}
	fmt.Println("\nThey arrive as notifications from the console. On each device you want them on,")
	fmt.Printf("allow notifications at %s\n", alertsPage(project))
	fmt.Printf("\n  gg alerts test %s     once a device is added, to see one arrive\n", project)
	return nil
}

func cmdAlertsTest(ref string) error {
	project, err := parseProject(ref)
	if err != nil {
		return err
	}
	var out struct {
		Sent int `json:"sent"`
	}
	if err := call("POST", "/v1/projects/"+project+"/alerts/test", nil, &out); err != nil {
		var ae apiError
		if errors.As(err, &ae) {
			switch ae.Code {
			case "alerts_off":
				return fmt.Errorf("alerts are off for %s, so there is nobody to send a test to\n  gg alerts on %s     to turn them on", project, project)
			case "no_devices":
				return fmt.Errorf("you have no device to send a test to\n  open %s in a browser and allow notifications", alertsPage(project))
			}
		}
		return err
	}
	fmt.Printf("sent to %d device(s)\n", out.Sent)
	fmt.Println("If nothing arrived, check that notifications are allowed for the console in that browser.")
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
