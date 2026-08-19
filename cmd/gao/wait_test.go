package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostLine writes one host the way the crawler records it, watched for an hour
// on server1 with four hundred fetches in flight.
func hostLine(name string, delay, robots, minGap, meanGap float64, peak, throttled int) string {
	return fmt.Sprintf(
		`{"host":%q,"box":"server1","fetches":840,"seconds":3600,"delay":%g,"robots":%g,`+
			`"min_gap":%g,"mean_gap":%g,"cap":2,"peak":%d,"load":400,"throttled":%d,"unavailable":1}`,
		name, delay, robots, minGap, meanGap, peak, throttled)
}

// waitRun writes a run that was polite unless a test says otherwise.
func waitRun(t *testing.T, lines ...string) string {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{
			hostLine("vnexpress.net", 4, 0, 4.1, 4.7, 2, 2),
			hostLine("tuoitre.vn", 4, 0, 4.4, 5.0, 2, 3),
			hostLine("diendan.example.vn", 4, 0, 5.2, 6.1, 2, 1),
		}
	}
	path := filepath.Join(t.TempDir(), "hosts.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestThePolitenessReportLeadsWithTheHostThatCameNearest(t *testing.T) {
	out, errOut, code := exec(t, "wait", waitRun(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"vnexpress.net", "server1", "gao-crawl-2026-09",
		"the crawl held its delay on 3 hosts",
		"under 400 fetches in flight on server1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
	rows := strings.Split(out, "\n")
	if !strings.HasPrefix(rows[1], "vnexpress.net") {
		t.Errorf("the table does not lead with the host that came nearest:\n%s", out)
	}
}

// The failure the command exists for, and the one a mean gap hides.
func TestADelayThatWasNotHeldExitsTwo(t *testing.T) {
	out, _, code := exec(t, "wait", waitRun(t,
		hostLine("vnexpress.net", 4, 0, 0.3, 4.9, 2, 2),
		hostLine("tuoitre.vn", 4, 0, 4.4, 5.0, 2, 3),
	))
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "the delay is a number in a config file and the socket did something else") {
		t.Errorf("the report does not say what a broken delay is:\n%s", out)
	}
}

// A cap held on the gap and not on the connections is the same load arriving at
// once.
func TestSixConnectionsHoldingEveryGapExitsTwo(t *testing.T) {
	out, _, code := exec(t, "wait", waitRun(t,
		hostLine("vnexpress.net", 4, 0, 4.1, 4.7, 6, 2),
		hostLine("tuoitre.vn", 4, 0, 4.4, 5.0, 2, 3),
	))
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "arriving in parallel instead of in sequence") {
		t.Errorf("the report accepts six connections to one host:\n%s", out)
	}
}

// The item's own words are on a real box under real load, and both halves are
// checked.
func TestAReadingOffAnIdleBoxExitsOne(t *testing.T) {
	idle := strings.Replace(hostLine("vnexpress.net", 4, 0, 4.1, 4.7, 2, 2), `"load":400`, `"load":2`, 1)
	out, _, code := exec(t, "wait", waitRun(t, idle, hostLine("tuoitre.vn", 4, 0, 4.4, 5.0, 2, 3)))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "a simulator with a real network stack under it") {
		t.Errorf("the report accepts a reading off an idle box:\n%s", out)
	}

	elsewhere := strings.Replace(hostLine("vnexpress.net", 4, 0, 4.1, 4.7, 2, 2), `"box":"server1"`, `"box":"server9"`, 1)
	out, _, code = exec(t, "wait", waitRun(t, elsewhere, hostLine("tuoitre.vn", 4, 0, 4.4, 5.0, 2, 3)))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "which is not a box on this fleet") {
		t.Errorf("the report accepts a machine nobody has:\n%s", out)
	}
}

func TestThePolitenessRunIsAlsoMachineReadable(t *testing.T) {
	out, errOut, code := exec(t, "wait", "-json", waitRun(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Crawl    string  `json:"crawl"`
		Hosts    int     `json:"hosts"`
		Load     int     `json:"load"`
		Broken   int     `json:"broken"`
		Overrun  int     `json:"overrun"`
		Refused  int     `json:"refused"`
		Closest  string  `json:"closest"`
		Margin   float64 `json:"margin"`
		Holds    bool    `json:"holds"`
		Readings []struct {
			Host     string  `json:"host"`
			Required float64 `json:"required"`
			MinGap   float64 `json:"min_gap"`
			Margin   float64 `json:"margin"`
			Errors   float64 `json:"errors"`
			Kept     bool    `json:"kept"`
		} `json:"readings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Crawl != "gao-crawl-2026-09" || got.Hosts != 3 || !got.Holds {
		t.Errorf("the run came back as %+v", got)
	}
	if got.Broken != 0 || got.Overrun != 0 || got.Refused != 0 {
		t.Errorf("broken %d, overrun %d, refused %d", got.Broken, got.Overrun, got.Refused)
	}
	if got.Closest != "vnexpress.net" || got.Load != 400 {
		t.Errorf("the closest host is %s at a load of %d", got.Closest, got.Load)
	}
	first := got.Readings[0]
	if first.Required != 4 || !first.Kept || first.Margin < 1.0 {
		t.Errorf("the closest reading came back as %+v", first)
	}
	if first.Errors < 0.003 || first.Errors > 0.004 {
		t.Errorf("three bad answers in 840 fetches came back as %.4f", first.Errors)
	}
}

func TestWaitRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "wait"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "wait", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "wait", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
}
