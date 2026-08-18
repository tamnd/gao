package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/gat"
)

// giaoReadings writes a readings file and returns the path to it.
func giaoReadings(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// giaoFleet is the readings taken off the S1 ingest runs on 2026-08-18, checked
// in as the file 'gao giao read' produced on each box.
//
// All three boxes can be handed work as the inventory reads on 2026-08-19.
// server3 was under the reserve for one inventory and drew nothing while
// carrying the fastest reading in the file, which is the reading it took off
// ingesting and publishing the whole GlotCC snapshot. It read 43.7 GB free at
// the retake and draws the largest share here. The file did not change. The
// disk did.
//
// It is a path into the repository rather than a temporary file, because the
// README quotes what this produces and a fixture whose input nobody can read is
// a fixture nobody can check.
func giaoFleet(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "giao", "testdata", "readings.jsonl")
}

func TestGiaoPlanPricesTheWholeIngestAgainstOneBox(t *testing.T) {
	out, errOut, code := exec(t, "giao", "plan", giaoFleet(t))
	if code != 0 {
		t.Fatalf("gao giao plan: exit %d, %s", code, errOut)
	}

	for _, want := range []string{
		"hplt3",
		"scratch left",
		"On the fastest box alone",
		"513.6 GB over 122 files across 3 boxes",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not say %q:\n%s", want, out)
		}
	}
	// The whole point of the barrier is that hplt3 is on its own line, before
	// everything that dedups against it.
	first := strings.Index(out, "hplt3")
	if later := strings.Index(out, "fineweb2"); later < first {
		t.Errorf("fineweb2 is printed before hplt3:\n%s", out)
	}
}

// A dropped box is dropped with its numbers, so that a reader can see that the
// reserve and not the disk is what took it out. A box that vanishes between the
// readings file and the schedule looks like a bug in the schedule.
//
// The reading is constructed rather than taken, because the only box the plan
// drops on the inventory of 2026-08-19 is server2, no corpus bytes land on
// server2, and so there is no ingest on it to read a rate off. Inventing a rate
// is the right call here and it would not be in giao/testdata/readings.jsonl:
// what is under test is the sentence the plan prints about a box it cannot use,
// and that sentence quotes the box's disk, which is real, rather than its rate.
func TestAPlanDropsABoxWithItsNumbersRatherThanQuietly(t *testing.T) {
	path := giaoReadings(t,
		`{"box":"server1","bytes":41000000000,"seconds":8480,"measured_on":"2026-08-18","how":"the fineweb2 ingest, off the run ledger"}`,
		`{"box":"server2","bytes":41000000000,"seconds":2409,"measured_on":"2026-08-19","how":"invented, because nothing has ever run here"}`,
	)
	out, errOut, code := exec(t, "giao", "plan", path)
	if code != 0 {
		t.Fatalf("gao giao plan: exit %d, %s", code, errOut)
	}

	for _, want := range []string{
		"server2 has a reading and 19.1 GB free",
		"0 bytes of scratch once the 20.0 GB reserve is taken off",
		"a fetch holds 1.0 GB while it runs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not say %q:\n%s", want, out)
		}
	}
	// The fast box is the one that was dropped, so the schedule has to be built
	// out of the slow one alone. A plan that quietly used the rate it was handed
	// would come back with two boxes and a shorter ingest than one box can do.
	if !strings.Contains(out, "over 122 files across 1 box ") {
		t.Errorf("the schedule is not built out of server1 alone:\n%s", out)
	}
}

func TestGiaoFilesNamesTheBoxForEveryFile(t *testing.T) {
	out, errOut, code := exec(t, "giao", "files", giaoFleet(t))
	if code != 0 {
		t.Fatalf("gao giao files: exit %d, %s", code, errOut)
	}

	var files int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, ".jsonl.zst") || strings.Contains(line, ".parquet") {
			files++
		}
	}
	if files != 122 {
		t.Errorf("the handover lists %d files, the manifest pins 122:\n%s", files, out)
	}

	// Every box named here is a box that may hold corpus bytes. What a file
	// costs a box is a fetch in flight and not the size of the file, which is
	// why a 26.6 GB HPLT shard sits on the same line as a 1.0 GB GlotCC one and
	// neither is routed around. server2 is the box the inventory drops, and the
	// thing to check is that no file is booked onto it.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "server2") {
			t.Errorf("server2 holds no corpus bytes and is handed a file: %s", line)
		}
	}
}

func TestGiaoPrintsTheSamePlanAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "giao", "plan", "-json", giaoFleet(t))
	if code != 0 {
		t.Fatalf("gao giao plan -json: exit %d, %s", code, errOut)
	}

	var got struct {
		Files   int     `json:"files"`
		Bytes   int64   `json:"bytes"`
		Seconds float64 `json:"seconds"`
		Holds   bool    `json:"holds"`
		Verdict string  `json:"verdict"`
		Groups  []struct {
			Order int `json:"order"`
			Hands []struct {
				Box   string `json:"box"`
				Files int    `json:"files"`
			} `json:"hands"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Files != gat.Files() || got.Bytes != gat.TotalBytes() {
		t.Errorf("the JSON covers %d files and %d bytes, the manifest pins %d and %d", got.Files, got.Bytes, gat.Files(), gat.TotalBytes())
	}
	if !got.Holds || got.Seconds <= 0 {
		t.Errorf("the JSON says holds=%v over %.0f seconds", got.Holds, got.Seconds)
	}
	if len(got.Groups) != 5 {
		t.Fatalf("the JSON has %d groups, the manifest pins five orders that are not dropped", len(got.Groups))
	}
	if got.Groups[0].Order != 0 {
		t.Errorf("the first group is order %d", got.Groups[0].Order)
	}
	if got.Verdict == "" {
		t.Error("the JSON has no verdict in it")
	}
}

func TestGiaoRefusesReadingsThatAreNotASchedule(t *testing.T) {
	path := giaoReadings(t, `{"box":"server2","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-03","how":"gao dem count over one shard"}`)

	out, _, code := exec(t, "giao", "plan", path)
	if code != 1 {
		t.Fatalf("gao giao plan on a box that may not hold corpus bytes: exit %d, want 1", code)
	}
	if !strings.Contains(out, "nowhere for the ingest to land") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	if strings.Contains(out, "takes") {
		t.Errorf("a refused plan still prints hours:\n%s", out)
	}
}

func TestGiaoExitsTwoOnAScheduleThatShouldNotBeRun(t *testing.T) {
	// server1 sixty thousand times slower than gamingpc is never the box that would
	// finish a file soonest, so it draws nothing and its reading bought nothing.
	// It has to be a box that may hold corpus bytes, since one that may not is
	// idle rather than at fault, and that is now two of the four.
	path := giaoReadings(t,
		`{"box":"gamingpc","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-18","how":"gao dem count over one shard"}`,
		`{"box":"server1","bytes":4200000000,"seconds":136000000,"measured_on":"2026-08-18","how":"gao dem count over one shard"}`,
	)

	out, _, code := exec(t, "giao", "plan", path)
	if code != 2 {
		t.Fatalf("gao giao plan on a box that draws nothing: exit %d, want 2", code)
	}
	if !strings.Contains(out, "This is not the schedule to run") || !strings.Contains(out, "server1 draws no files") {
		t.Errorf("the plan does not name the fault:\n%s", out)
	}
}

func TestGiaoSaysWhichLineOfTheReadingsIsWrong(t *testing.T) {
	path := giaoReadings(t,
		`{"box":"gamingpc","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-03","how":"gao dem count over one shard"}`,
		`{"box":"server1","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-03","cores":4}`,
	)

	_, errOut, code := exec(t, "giao", "plan", path)
	if code != 1 {
		t.Fatalf("gao giao plan on a readings file it cannot read: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, ":2:") || !strings.Contains(errOut, "cores") {
		t.Errorf("the error does not say which line or which field: %q", errOut)
	}
}

func TestGiaoWithoutASubcommandSaysWhatItTakes(t *testing.T) {
	for _, args := range [][]string{
		{"giao"},
		{"giao", "schedule"},
		{"giao", "plan"},
		{"giao", "plan", "one.jsonl", "two.jsonl"},
	} {
		_, errOut, code := exec(t, args...)
		if code != 2 {
			t.Errorf("gao %s: exit %d, want 2", strings.Join(args, " "), code)
		}
		if errOut == "" {
			t.Errorf("gao %s: exit 2 with nothing on stderr", strings.Join(args, " "))
		}
	}
}

func TestGiaoHelpSaysWhatAReadingIs(t *testing.T) {
	out, _, code := exec(t, "giao", "help")
	if code != 0 {
		t.Fatalf("gao giao help: exit %d", code)
	}
	// A schedule off a link speed is the mistake this command exists to stop,
	// so the help has to say so before anybody writes a readings file.
	if !strings.Contains(out, "Not a link speed") {
		t.Errorf("the help does not say what a reading is:\n%s", out)
	}
}

// ledgerWith writes an ingest ledger the way a run leaves one, and returns the
// directory it is in.
func ledgerWith(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, gat.LedgerName)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGiaoReadTurnsALedgerIntoAReading(t *testing.T) {
	dir := ledgerWith(t,
		`{"source":"glotcc","revision":"9ad140b6be3a","path":"v1.0/vie-Latn/vie-Latn_0.parquet","bytes":2100000000,"digest":"","documents":900000,"reconnects":0,"finished":"2026-08-18T06:19:00Z","box":"server3"}`,
		`{"source":"glotcc","revision":"9ad140b6be3a","path":"v1.0/vie-Latn/vie-Latn_1.parquet","bytes":2100000000,"digest":"","documents":900000,"reconnects":0,"finished":"2026-08-18T06:39:00Z","box":"server3"}`,
	)

	out, errOut, code := exec(t, "giao", "read", "-dir", dir)
	if code != 0 {
		t.Fatalf("gao giao read: exit %d, %s", code, errOut)
	}

	var r struct {
		Box     string  `json:"box"`
		Bytes   int64   `json:"bytes"`
		Seconds float64 `json:"seconds"`
		On      string  `json:"measured_on"`
		How     string  `json:"how"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("the reading is not JSON: %v\n%s", err, out)
	}
	if r.Box != "server3" || r.Bytes != 2100000000 || r.Seconds != 1200 {
		t.Errorf("read %+v, want server3 across the one file whose window the ledger knows", r)
	}
	if r.On != "2026-08-18" || !strings.Contains(r.How, "glotcc") {
		t.Errorf("read %+v, want the date of the run and what it fetched", r)
	}
}

// What the command is for is that the line it prints goes straight into the
// readings a schedule is planned from.
func TestGiaoReadPrintsALineThatPlanAccepts(t *testing.T) {
	dir := ledgerWith(t,
		`{"source":"fineweb2","revision":"af9c13333eb9","path":"data/vie_Latn/train/000_00000.parquet","bytes":4300000000,"digest":"","documents":1200000,"reconnects":0,"finished":"2026-08-18T11:00:00Z","box":"server1"}`,
		`{"source":"fineweb2","revision":"af9c13333eb9","path":"data/vie_Latn/train/000_00001.parquet","bytes":4300000000,"digest":"","documents":1200000,"reconnects":0,"finished":"2026-08-18T12:00:00Z","box":"server1"}`,
	)
	out, _, code := exec(t, "giao", "read", "-dir", dir)
	if code != 0 {
		t.Fatalf("gao giao read: exit %d", code)
	}

	path := giaoReadings(t, strings.TrimSpace(out))
	if _, errOut, code := exec(t, "giao", "plan", path); code != 0 {
		t.Fatalf("gao giao plan refused a reading gao giao read produced: exit %d, %s", code, errOut)
	}
}

func TestGiaoReadRefusesALedgerThatIsNotAReading(t *testing.T) {
	dir := ledgerWith(t,
		`{"source":"glotcc","revision":"9ad140b6be3a","path":"v1.0/vie-Latn/vie-Latn_0.parquet","bytes":2100000000,"digest":"","documents":900000,"reconnects":0,"finished":"2026-08-18T06:19:00Z","box":"server3"}`,
	)
	out, errOut, code := exec(t, "giao", "read", "-dir", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1 for a ledger that carries no reading", code)
	}
	if out != "" {
		t.Errorf("it printed a reading anyway: %s", out)
	}
	if !strings.Contains(errOut, "one finish time is not a duration") {
		t.Errorf("stderr says %q, and it should say why", errOut)
	}
}

func TestGiaoReadNeedsADirectory(t *testing.T) {
	if _, _, code := exec(t, "giao", "read"); code != 2 {
		t.Errorf("exit %d, want 2 without -dir", code)
	}
}
