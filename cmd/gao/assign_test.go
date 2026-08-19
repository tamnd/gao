package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/harvest"
)

// pinnedSize is the size of the ingest as the manifest currently has it, in the
// words the verdict uses. It is computed rather than written down because a
// source leaving the fetch list moves both halves of it, and a test that pins
// the old pair reads as the plan having broken.
func pinnedSize() string {
	return fmt.Sprintf("%s over %d files", fleet.GB(harvest.TotalBytes()), harvest.Files())
}

// pinnedOrders is how many ingest orders have a source left in them, which is
// how many groups a plan comes back with.
func pinnedOrders() int {
	var orders []int
	for _, p := range harvest.Sources() {
		if !slices.Contains(orders, p.Order) {
			orders = append(orders, p.Order)
		}
	}
	return len(orders)
}

// assignReadings writes a readings file and returns the path to it.
func assignReadings(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// assignFleet is the readings taken off the S1 ingest runs on 2026-08-18, checked
// in as the file 'gao assign read' produced on each box.
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
func assignFleet(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "assign", "testdata", "readings.jsonl")
}

func TestAssignPlanPricesTheWholeIngestAgainstOneBox(t *testing.T) {
	out, errOut, code := exec(t, "assign", "plan", assignFleet(t))
	if code != 0 {
		t.Fatalf("gao assign plan: exit %d, %s", code, errOut)
	}

	for _, want := range []string{
		"hplt3",
		"scratch left",
		"On the fastest box alone",
		pinnedSize() + " across 3 boxes",
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
// is the right call here and it would not be in assign/testdata/readings.jsonl:
// what is under test is the sentence the plan prints about a box it cannot use,
// and that sentence quotes the box's disk, which is real, rather than its rate.
func TestAPlanDropsABoxWithItsNumbersRatherThanQuietly(t *testing.T) {
	path := assignReadings(t,
		`{"box":"server1","bytes":41000000000,"seconds":8480,"measured_on":"2026-08-18","how":"the fineweb2 ingest, off the run ledger"}`,
		`{"box":"server2","bytes":41000000000,"seconds":2409,"measured_on":"2026-08-19","how":"invented, because nothing has ever run here"}`,
	)
	out, errOut, code := exec(t, "assign", "plan", path)
	if code != 0 {
		t.Fatalf("gao assign plan: exit %d, %s", code, errOut)
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
	if !strings.Contains(out, pinnedSize()+" across 1 box ") {
		t.Errorf("the schedule is not built out of server1 alone:\n%s", out)
	}
}

func TestAssignFilesNamesTheBoxForEveryFile(t *testing.T) {
	out, errOut, code := exec(t, "assign", "files", assignFleet(t))
	if code != 0 {
		t.Fatalf("gao assign files: exit %d, %s", code, errOut)
	}

	var files int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, ".jsonl.zst") || strings.Contains(line, ".parquet") {
			files++
		}
	}
	if files != harvest.Files() {
		t.Errorf("the handover lists %d files, the manifest pins %d:\n%s", files, harvest.Files(), out)
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

// One box's share, in the form the fetcher takes. Everything that makes the
// table readable is left out on purpose, because this output is read by
// 'gao harvest hf -only' rather than by a person.
func TestAssignFilesWritesOneBoxsListAndNothingElse(t *testing.T) {
	out, errOut, code := exec(t, "assign", "files", "-box", "server3", assignFleet(t))
	if code != 0 {
		t.Fatalf("gao assign files -box server3: exit %d, %s", code, errOut)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("server3 draws the largest share of the ingest and its list is empty")
	}
	for _, line := range lines {
		if len(strings.Fields(line)) != 1 {
			t.Errorf("a line carries something besides the name, which the fetcher would read as a file: %q", line)
		}
		if !strings.Contains(line, "/") {
			t.Errorf("a name is not source and path joined by a slash: %q", line)
		}
	}
	for _, unwanted := range []string{"box", "takes", "hours", "The whole ingest"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the list carries %q, and it is meant to carry names only:\n%s", unwanted, out)
		}
	}

	// The same files the table says, which is the claim that makes the list worth
	// handing to a box.
	table, _, code := exec(t, "assign", "files", assignFleet(t))
	if code != 0 {
		t.Fatalf("gao assign files: exit %d", code)
	}
	var counted int
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, "server3") {
			counted++
		}
	}
	if counted != len(lines) {
		t.Errorf("the list has %d files and the table books %d onto server3", len(lines), counted)
	}
}

// A box the schedule dropped and a box that is not in the schedule at all both
// exit 2, and they say different things. Printing an empty list for either would
// start a run that fetches nothing and reads as a box that is already up to date.
//
// The dropped case needs a readings file with server2 in it, since a box with no
// reading is not a box the split dropped, it is a box nobody measured. That is
// the same constructed reading TestAPlanDropsABoxWithItsNumbersRatherThanQuietly
// uses and it is invented for the same reason: nothing has ever run on server2.
func TestAssignFilesRefusesABoxItHasNoWorkFor(t *testing.T) {
	dropped := assignReadings(t,
		`{"box":"server1","bytes":41000000000,"seconds":8480,"measured_on":"2026-08-18","how":"the fineweb2 ingest, off the run ledger"}`,
		`{"box":"server2","bytes":41000000000,"seconds":2409,"measured_on":"2026-08-19","how":"invented, because nothing has ever run here"}`,
	)
	for _, tc := range []struct {
		box      string
		readings string
		want     string
	}{
		{box: "server2", readings: dropped, want: "draws nothing"},
		{box: "server2", readings: assignFleet(t), want: "is not a box this schedule hands work to"},
		{box: "laptop", readings: assignFleet(t), want: "is not a box this schedule hands work to"},
	} {
		t.Run(tc.box+" "+tc.want, func(t *testing.T) {
			out, errOut, code := exec(t, "assign", "files", "-box", tc.box, tc.readings)
			if code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Errorf("the refusal does not say %q: %q", tc.want, errOut)
			}
			if strings.TrimSpace(out) != "" {
				t.Errorf("a refused box still got a list:\n%s", out)
			}
		})
	}
}

// The list and the JSON are two answers to the same question and only one of
// them can be on stdout at a time, so asking for both is a mistake rather than a
// preference the command should pick from.
func TestAssignFilesRefusesABoxAndJSONTogether(t *testing.T) {
	_, errOut, code := exec(t, "assign", "files", "-box", "server3", "-json", assignFleet(t))
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut, "-box") || !strings.Contains(errOut, "-json") {
		t.Errorf("the refusal does not name both flags: %q", errOut)
	}
}

// 'assign plan' prices the schedule and 'assign files' is the schedule, so the
// file list is in one of them and not the other. A hundred and twenty two
// entries under a command somebody ran to read five summary rows would bury
// the rows.
func TestOnlyAssignFilesCarriesTheJobsInItsJSON(t *testing.T) {
	type hand struct {
		Box  string `json:"box"`
		Jobs []struct {
			Source  string  `json:"source"`
			Path    string  `json:"path"`
			Name    string  `json:"name"`
			Bytes   int64   `json:"bytes"`
			Seconds float64 `json:"seconds"`
		} `json:"jobs"`
	}
	read := func(t *testing.T, args ...string) []hand {
		t.Helper()
		out, errOut, code := exec(t, args...)
		if code != 0 {
			t.Fatalf("gao %s: exit %d, %s", strings.Join(args, " "), code, errOut)
		}
		var got struct {
			Groups []struct {
				Hands []hand `json:"hands"`
			} `json:"groups"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		var hands []hand
		for _, g := range got.Groups {
			hands = append(hands, g.Hands...)
		}
		return hands
	}

	for _, h := range read(t, "giao", "plan", "-json", assignFleet(t)) {
		if len(h.Jobs) != 0 {
			t.Errorf("the plan carries %d files for %s, and it is a summary", len(h.Jobs), h.Box)
		}
	}

	var files int
	for _, h := range read(t, "giao", "files", "-json", assignFleet(t)) {
		for _, j := range h.Jobs {
			files++
			if j.Name != j.Source+"/"+j.Path {
				t.Errorf("the name %q is not the source and the path joined by a slash", j.Name)
			}
			if j.Bytes <= 0 || j.Seconds <= 0 {
				t.Errorf("%s is booked at %d bytes and %.1f seconds", j.Name, j.Bytes, j.Seconds)
			}
		}
	}
	if files != harvest.Files() {
		t.Errorf("the JSON names %d files, the manifest pins %d", files, harvest.Files())
	}
}

func TestAssignPrintsTheSamePlanAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "assign", "plan", "-json", assignFleet(t))
	if code != 0 {
		t.Fatalf("gao assign plan -json: exit %d, %s", code, errOut)
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
	if got.Files != harvest.Files() || got.Bytes != harvest.TotalBytes() {
		t.Errorf("the JSON covers %d files and %d bytes, the manifest pins %d and %d", got.Files, got.Bytes, harvest.Files(), harvest.TotalBytes())
	}
	if !got.Holds || got.Seconds <= 0 {
		t.Errorf("the JSON says holds=%v over %.0f seconds", got.Holds, got.Seconds)
	}
	if len(got.Groups) != pinnedOrders() {
		t.Fatalf("the JSON has %d groups, the manifest pins %d orders that are not dropped", len(got.Groups), pinnedOrders())
	}
	if got.Groups[0].Order != 0 {
		t.Errorf("the first group is order %d", got.Groups[0].Order)
	}
	if got.Verdict == "" {
		t.Error("the JSON has no verdict in it")
	}
}

func TestAssignRefusesReadingsThatAreNotASchedule(t *testing.T) {
	path := assignReadings(t, `{"box":"server2","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-03","how":"gao count count over one shard"}`)

	out, _, code := exec(t, "assign", "plan", path)
	if code != 1 {
		t.Fatalf("gao assign plan on a box that may not hold corpus bytes: exit %d, want 1", code)
	}
	if !strings.Contains(out, "nowhere for the ingest to land") {
		t.Errorf("the refusal does not say why:\n%s", out)
	}
	if strings.Contains(out, "takes") {
		t.Errorf("a refused plan still prints hours:\n%s", out)
	}
}

func TestAssignExitsTwoOnAScheduleThatShouldNotBeRun(t *testing.T) {
	// server1 sixty thousand times slower than gamingpc is never the box that would
	// finish a file soonest, so it draws nothing and its reading bought nothing.
	// It has to be a box that may hold corpus bytes, since one that may not is
	// idle rather than at fault, and that is now two of the four.
	path := assignReadings(t,
		`{"box":"gamingpc","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-18","how":"gao count count over one shard"}`,
		`{"box":"server1","bytes":4200000000,"seconds":136000000,"measured_on":"2026-08-18","how":"gao count count over one shard"}`,
	)

	out, _, code := exec(t, "assign", "plan", path)
	if code != 2 {
		t.Fatalf("gao assign plan on a box that draws nothing: exit %d, want 2", code)
	}
	if !strings.Contains(out, "This is not the schedule to run") || !strings.Contains(out, "server1 draws no files") {
		t.Errorf("the plan does not name the fault:\n%s", out)
	}
}

func TestAssignSaysWhichLineOfTheReadingsIsWrong(t *testing.T) {
	path := assignReadings(t,
		`{"box":"gamingpc","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-03","how":"gao count count over one shard"}`,
		`{"box":"server1","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-03","cores":4}`,
	)

	_, errOut, code := exec(t, "assign", "plan", path)
	if code != 1 {
		t.Fatalf("gao assign plan on a readings file it cannot read: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, ":2:") || !strings.Contains(errOut, "cores") {
		t.Errorf("the error does not say which line or which field: %q", errOut)
	}
}

func TestAssignWithoutASubcommandSaysWhatItTakes(t *testing.T) {
	for _, args := range [][]string{
		{"assign"},
		{"assign", "schedule"},
		{"assign", "plan"},
		{"assign", "plan", "one.jsonl", "two.jsonl"},
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

func TestAssignHelpSaysWhatAReadingIs(t *testing.T) {
	out, _, code := exec(t, "assign", "help")
	if code != 0 {
		t.Fatalf("gao assign help: exit %d", code)
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
	path := filepath.Join(dir, harvest.LedgerName)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAssignReadTurnsALedgerIntoAReading(t *testing.T) {
	dir := ledgerWith(t,
		`{"source":"glotcc","revision":"9ad140b6be3a","path":"v1.0/vie-Latn/vie-Latn_0.parquet","bytes":2100000000,"digest":"","documents":900000,"reconnects":0,"finished":"2026-08-18T06:19:00Z","box":"server3"}`,
		`{"source":"glotcc","revision":"9ad140b6be3a","path":"v1.0/vie-Latn/vie-Latn_1.parquet","bytes":2100000000,"digest":"","documents":900000,"reconnects":0,"finished":"2026-08-18T06:39:00Z","box":"server3"}`,
	)

	out, errOut, code := exec(t, "assign", "read", "-dir", dir)
	if code != 0 {
		t.Fatalf("gao assign read: exit %d, %s", code, errOut)
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
func TestAssignReadPrintsALineThatPlanAccepts(t *testing.T) {
	dir := ledgerWith(t,
		`{"source":"fineweb2","revision":"af9c13333eb9","path":"data/vie_Latn/train/000_00000.parquet","bytes":4300000000,"digest":"","documents":1200000,"reconnects":0,"finished":"2026-08-18T11:00:00Z","box":"server1"}`,
		`{"source":"fineweb2","revision":"af9c13333eb9","path":"data/vie_Latn/train/000_00001.parquet","bytes":4300000000,"digest":"","documents":1200000,"reconnects":0,"finished":"2026-08-18T12:00:00Z","box":"server1"}`,
	)
	out, _, code := exec(t, "assign", "read", "-dir", dir)
	if code != 0 {
		t.Fatalf("gao assign read: exit %d", code)
	}

	path := assignReadings(t, strings.TrimSpace(out))
	if _, errOut, code := exec(t, "assign", "plan", path); code != 0 {
		t.Fatalf("gao assign plan refused a reading gao assign read produced: exit %d, %s", code, errOut)
	}
}

func TestAssignReadRefusesALedgerThatIsNotAReading(t *testing.T) {
	dir := ledgerWith(t,
		`{"source":"glotcc","revision":"9ad140b6be3a","path":"v1.0/vie-Latn/vie-Latn_0.parquet","bytes":2100000000,"digest":"","documents":900000,"reconnects":0,"finished":"2026-08-18T06:19:00Z","box":"server3"}`,
	)
	out, errOut, code := exec(t, "assign", "read", "-dir", dir)
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

func TestAssignReadNeedsADirectory(t *testing.T) {
	if _, _, code := exec(t, "assign", "read"); code != 2 {
		t.Errorf("exit %d, want 2 without -dir", code)
	}
}
