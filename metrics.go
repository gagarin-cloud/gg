package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
)

// gg metrics reads what the engine measures about a project's services.
//
// With a project it is one row per service: how much of its CPU, memory and
// disk it uses now, and for the ones with a public address how much traffic
// they get. With a service it is the window summarised — the last value, the
// average and the peak of each series, against the limit where there is one —
// not the points themselves.
//
// Any figure may be null on the wire: it was not measured, which is not zero.
// It prints as "-".

type usage struct {
	Used  *float64 `json:"used"`
	Limit *float64 `json:"limit"`
}

type serviceMetrics struct {
	Service    string   `json:"service"`
	Kind       string   `json:"kind"`
	CPU        usage    `json:"cpu"`
	Memory     usage    `json:"memory"`
	Disk       usage    `json:"disk"`
	Throttled  *float64 `json:"throttled"`
	NetIn      *float64 `json:"net_in"`
	NetOut     *float64 `json:"net_out"`
	Requests   *float64 `json:"requests"`
	Errors     *float64 `json:"errors"`
	LatencyP95 *float64 `json:"latency_p95"`
}

type metricsResp struct {
	Services []serviceMetrics `json:"services"`
	Windows  []string         `json:"windows"`
}

type metricsRangeResp struct {
	Service string                  `json:"service"`
	Window  string                  `json:"window"`
	Step    int                     `json:"step"`
	Limits  map[string]*float64     `json:"limits"`
	Series  map[string][][2]float64 `json:"series"`
}

// metricsUnit says how a figure is written. Series are shown in the order the
// engine's units are most useful to read, not alphabetically.
type metricsUnit int

const (
	unitCores metricsUnit = iota
	unitBytes
	unitFraction
	unitBytesPerSec
	unitReqPerSec
	unitSeconds
)

var metricsSeriesOrder = []struct {
	key, label string
	unit       metricsUnit
}{
	{"cpu", "cpu", unitCores},
	{"memory", "memory", unitBytes},
	{"disk", "disk", unitBytes},
	{"throttled", "throttled", unitFraction},
	{"net_in", "net in", unitBytesPerSec},
	{"net_out", "net out", unitBytesPerSec},
	{"requests", "requests", unitReqPerSec},
	{"errors", "5xx", unitFraction},
	{"latency_p95", "p95", unitSeconds},
}

func fetchMetrics(project string) (metricsResp, error) {
	var out metricsResp
	err := call("GET", "/v1/projects/"+project+"/metrics", nil, &out)
	return out, err
}

// metricsRangeQuery sends the window only when one was asked for: the default
// is the server's.
func metricsRangeQuery(window string) string {
	if window == "" {
		return ""
	}
	return "?" + url.Values{"window": {window}}.Encode()
}

func fetchMetricsRange(project, service, window string) (metricsRangeResp, error) {
	var out metricsRangeResp
	err := call("GET", "/v1/projects/"+project+"/services/"+service+"/metrics"+metricsRangeQuery(window), nil, &out)
	return out, err
}

// cmdMetrics takes `shop` for the whole project or `shop/web` for one service.
func cmdMetrics(ref, window string) error {
	if !strings.Contains(ref, "/") {
		project, err := parseProject(ref)
		if err != nil {
			return err
		}
		if window != "" {
			return fmt.Errorf("--window is for one service's history\n  hint: gg metrics %s/SERVICE --window %s", project, window)
		}
		out, err := fetchMetrics(project)
		if err != nil {
			return err
		}
		printMetricsTable(project, out)
		return nil
	}
	project, service, _, err := parseService(ref)
	if err != nil {
		return err
	}
	out, err := fetchMetricsRange(project, service, window)
	if err != nil {
		return err
	}
	printMetricsSummary(project, out)
	return nil
}

// --- formatting ------------------------------------------------------------

func trimFloat(v float64, prec int) string {
	s := fmt.Sprintf("%.*f", prec, v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

func fmtBytes(v float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return trimFloat(v, 0) + " B"
	}
	return trimFloat(v, 1) + " " + units[i]
}

func fmtMetric(v float64, u metricsUnit) string {
	switch u {
	case unitCores:
		return trimFloat(v, 2)
	case unitBytes:
		return fmtBytes(v)
	case unitFraction:
		return trimFloat(v*100, 1) + "%"
	case unitBytesPerSec:
		return fmtBytes(v) + "/s"
	case unitReqPerSec:
		return trimFloat(v, 2) + "/s"
	case unitSeconds:
		if v < 1 {
			return trimFloat(v*1000, 0) + " ms"
		}
		return trimFloat(v, 2) + " s"
	}
	return trimFloat(v, 2)
}

func pct(v, limit float64) int {
	return int(v/limit*100 + 0.5)
}

// fmtUsage is "used/limit (pct%)", or just the used figure when there is no
// limit, and "-" when nothing was measured.
func fmtUsage(x usage, u metricsUnit) string {
	if x.Used == nil {
		return "-"
	}
	s := fmtMetric(*x.Used, u)
	if x.Limit == nil || *x.Limit <= 0 {
		return s
	}
	return fmt.Sprintf("%s/%s (%d%%)", s, fmtMetric(*x.Limit, u), pct(*x.Used, *x.Limit))
}

func fmtOpt(v *float64, u metricsUnit) string {
	if v == nil {
		return "-"
	}
	return fmtMetric(*v, u)
}

func printMetricsTable(project string, m metricsResp) {
	fmt.Printf("\nproject %s\n\n", project)
	if len(m.Services) == 0 {
		fmt.Printf("  nothing to measure yet — ship a service with `gg ship %s/web:8080`\n\n", project)
		return
	}
	// The HTTP columns only when something has an address to measure them on.
	anyHTTP := false
	for _, s := range m.Services {
		if s.Requests != nil || s.Errors != nil || s.LatencyP95 != nil {
			anyHTTP = true
		}
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	head := "  SERVICE\tCPU\tMEMORY\tDISK"
	if anyHTTP {
		head += "\tREQ/S\t5XX\tP95"
	}
	fmt.Fprintln(tw, head)
	for _, s := range m.Services {
		row := fmt.Sprintf("  %s\t%s\t%s\t%s", s.Service,
			fmtUsage(s.CPU, unitCores), fmtUsage(s.Memory, unitBytes), fmtUsage(s.Disk, unitBytes))
		if anyHTTP {
			row += fmt.Sprintf("\t%s\t%s\t%s", fmtOpt(s.Requests, unitReqPerSec),
				fmtOpt(s.Errors, unitFraction), fmtOpt(s.LatencyP95, unitSeconds))
		}
		fmt.Fprintln(tw, row)
	}
	_ = tw.Flush()
	fmt.Printf("\n  `gg metrics %s/SERVICE` shows one service over time (--window 1h, 24h, 7d, 30d)\n\n", project)
}

// seriesSummary is one series reduced to what a person reads off a graph.
type seriesSummary struct {
	Last, Avg, Peak float64
}

func summarize(points [][2]float64) (seriesSummary, bool) {
	if len(points) == 0 {
		return seriesSummary{}, false
	}
	s := seriesSummary{Last: points[len(points)-1][1], Peak: points[0][1]}
	sum := 0.0
	for _, p := range points {
		sum += p[1]
		if p[1] > s.Peak {
			s.Peak = p[1]
		}
	}
	s.Avg = sum / float64(len(points))
	return s, true
}

func printMetricsSummary(project string, m metricsRangeResp) {
	window := m.Window
	if window == "" {
		window = "24h"
	}
	fmt.Printf("\n%s/%s, last %s (%ds steps)\n\n", project, m.Service, window, m.Step)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "  \tNOW\tAVG\tPEAK\tLIMIT\tPEAK/LIMIT")
	rows := 0
	for _, d := range metricsSeriesOrder {
		s, ok := summarize(m.Series[d.key])
		if !ok {
			continue
		}
		limit, peakPct := "-", "-"
		if l := m.Limits[d.key]; l != nil && *l > 0 {
			limit = fmtMetric(*l, d.unit)
			peakPct = fmt.Sprintf("%d%%", pct(s.Peak, *l))
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", d.label,
			fmtMetric(s.Last, d.unit), fmtMetric(s.Avg, d.unit), fmtMetric(s.Peak, d.unit), limit, peakPct)
		rows++
	}
	if rows == 0 {
		fmt.Printf("  nothing measured for this service in the last %s\n\n", window)
		return
	}
	_ = tw.Flush()
	fmt.Println()
}
