package main

import (
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

func TestParseTimeout(t *testing.T) {
	for in, want := range map[string]int{
		"1s":    1,
		"45s":   45,
		"5m":    300,
		"1h":    3600,
		"60m":   3600,
		"1m30s": 90,
	} {
		t.Run(in, func(t *testing.T) {
			got, err := parseTimeout(in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != want {
				t.Errorf("got %d, want %d", got, want)
			}
		})
	}
}

func TestParseTimeoutRefuses(t *testing.T) {
	for _, in := range []string{"1.5s", "500ms", "0s", "-5s", "61m", "3601s", "2h", "abc", "90"} {
		t.Run(in, func(t *testing.T) {
			if got, err := parseTimeout(in); err == nil {
				t.Errorf("accepted %q as %d seconds", in, got)
			}
		})
	}
}

func TestRunFlagsFinishTimeout(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    int
		wantErr bool
	}{
		{nil, 0, false},
		{[]string{"--timeout", "5m"}, 300, false},
		{[]string{"--timeout", "2h"}, 0, true},
	} {
		fs := pflag.NewFlagSet("run", pflag.ContinueOnError)
		v := bindRunFlags(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		f, err := v.finish()
		if (err != nil) != tc.wantErr {
			t.Fatalf("%v: err = %v", tc.args, err)
		}
		if err == nil && f.timeout != tc.want {
			t.Errorf("%v: timeout = %d, want %d", tc.args, f.timeout, tc.want)
		}
	}
}

func TestRunBodyCarriesTimeoutOnlyWhenSet(t *testing.T) {
	without := runBody("img", "sha256:x", &runFlags{env: map[string]string{}})
	if _, ok := without["timeout_seconds"]; ok {
		t.Errorf("timeout_seconds sent when --timeout was not given: %v", without)
	}
	with := runBody("img", "sha256:x", &runFlags{env: map[string]string{}, timeout: 90})
	if got := with["timeout_seconds"]; !reflect.DeepEqual(got, 90) {
		t.Errorf("timeout_seconds = %#v, want 90", got)
	}
}
