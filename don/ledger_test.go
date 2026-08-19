package don

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// start is a fixed instant so that a log reads the same on every box and in
// every year, which is the only reason a time appears in a test at all.
var start = time.Date(2026, 9, 14, 3, 0, 0, 0, time.UTC)

// life is one file taking every step in order, which is what the rotation is
// supposed to produce and is the baseline everything else is a deviation from.
func life(name string, bytes int64, through State) []Event {
	var out []Event
	for s := Resident; s <= through; s++ {
		out = append(out, Event{
			Name:  name,
			Path:  "data/gao-crawl-2026-09/" + name,
			Bytes: bytes,
			Hash:  "1f4a" + name,
			State: s,
			At:    start.Add(time.Duration(s) * 4 * time.Minute),
			Box:   "server1",
		})
	}
	return out
}

func TestAFileThatTookEveryStepIsSound(t *testing.T) {
	l := Read(life("part-00001.warc.gz", 1_000_000_000, Reclaimed))
	if !l.Sound() {
		t.Fatalf("a clean rotation reported faults: %v", l.Faults)
	}
	if l.Reclaimed() != 1_000_000_000 {
		t.Errorf("freed %d bytes", l.Reclaimed())
	}
	if l.OnDisk() != 0 {
		t.Errorf("%d bytes still on the box after a reclaim", l.OnDisk())
	}
	if got := l.Files[0].Held(); got != 12*time.Minute {
		t.Errorf("the file was held for %s", got)
	}
}

// This is the fault the package exists for. A box that deleted something it
// never confirmed looks exactly like a box that behaved, because in both cases
// the file is gone, and the log is the only place the difference survives.
func TestDeletingWithoutConfirmingIsTheFaultThatMatters(t *testing.T) {
	events := life("part-00002.warc.gz", 900_000_000, Reclaimed)
	events = without(events, Verified)

	l := Read(events)
	if l.Sound() {
		t.Fatal("a file deleted without confirmation was reported as sound")
	}
	if !strings.Contains(l.Faults[0], "the log cannot say which") {
		t.Errorf("the fault does not say what was lost: %s", l.Faults[0])
	}
	if !strings.Contains(l.Verdict(), "cannot be trusted") {
		t.Errorf("the verdict is too kind: %s", l.Verdict())
	}
}

// A verification that never had an upload behind it passed against whatever was
// already sitting at that path, which is a check that proves nothing and reads
// as if it proved everything.
func TestVerifyingSomethingThatWasNeverUploaded(t *testing.T) {
	events := without(life("part-00003.warc.gz", 800_000_000, Verified), Pushed)

	l := Read(events)
	if l.Sound() {
		t.Fatal("a verification with no upload behind it was accepted")
	}
	if !strings.Contains(l.Faults[0], "already in the store under that path") {
		t.Errorf("the fault does not say what actually happened: %s", l.Faults[0])
	}
}

// Two hashes for one file is the local copy and the store copy disagreeing,
// which is the one case where the upload succeeded and the bytes are still
// wrong.
func TestAFileReportedWithTwoHashesIsNotVerified(t *testing.T) {
	events := life("part-00004.warc.gz", 700_000_000, Reclaimed)
	events[2].Hash = "9c02different"

	l := Read(events)
	if l.Sound() {
		t.Fatal("a file with two hashes was reported as sound")
	}
	if !strings.Contains(strings.Join(l.Faults, " "), "not the copy this box wrote") {
		t.Errorf("the fault does not name the disagreement: %v", l.Faults)
	}
}

// A file that went somewhere without recording where is a file nobody can go
// and look for.
func TestAFileWithNoPathInTheStoreIsAFault(t *testing.T) {
	events := life("part-00005.warc.gz", 600_000_000, Pushed)
	for i := range events {
		events[i].Path = ""
	}

	l := Read(events)
	if l.Sound() {
		t.Fatal("a file pushed to nowhere in particular was accepted")
	}
	if !strings.Contains(l.Faults[0], "nothing here says where it went") {
		t.Errorf("the fault does not say what is missing: %s", l.Faults[0])
	}
}

// The backlog is what the arithmetic predicts, so the log has to report it in
// the same terms or the two can never be compared.
func TestTheBacklogTheLogReportsIsTheOneTheArithmeticPredicts(t *testing.T) {
	events := make([]Event, 0, 16)
	events = append(events, life("part-00010.warc.gz", 1_000_000_000, Reclaimed)...)
	events = append(events, life("part-00011.warc.gz", 1_000_000_000, Pushed)...)
	events = append(events, life("part-00012.warc.gz", 1_000_000_000, Resident)...)
	events = append(events, life("part-00013.warc.gz", 1_000_000_000, Verified)...)

	l := Read(events)
	if !l.Sound() {
		t.Fatalf("faults in a clean log: %v", l.Faults)
	}
	if got := l.Unsafe(); got != 2_000_000_000 {
		t.Errorf("%d bytes not yet safe to delete, want 2000000000", got)
	}
	if got := l.OnDisk(); got != 3_000_000_000 {
		t.Errorf("%d bytes on the box, want 3000000000", got)
	}
	if got := l.Bytes(); got != 4_000_000_000 {
		t.Errorf("%d bytes accounted for, want 4000000000", got)
	}
	for state, want := range map[State]int{Resident: 1, Pushed: 1, Verified: 1, Reclaimed: 1} {
		if got := l.Count(state); got != want {
			t.Errorf("%d files at %s, want %d", got, state, want)
		}
	}
}

// A rotation that has quietly stopped shows up as the oldest thing still
// sitting there, which is findable without reading six weeks of log by eye.
func TestTheOldestThingStillOnTheBoxIsFindable(t *testing.T) {
	stuck := life("part-00020.warc.gz", 1_000_000_000, Resident)
	fresh := life("part-00021.warc.gz", 1_000_000_000, Pushed)
	for i := range fresh {
		fresh[i].At = fresh[i].At.Add(72 * time.Hour)
	}

	l := Read(append(fresh, stuck...))
	got, ok := l.Oldest()
	if !ok {
		t.Fatal("nothing was reported as being on the box")
	}
	if got.Name != "part-00020.warc.gz" {
		t.Errorf("the oldest file on the box is %s", got.Name)
	}

	done := Read(life("part-00022.warc.gz", 1, Reclaimed))
	if _, ok := done.Oldest(); ok {
		t.Error("a fully reclaimed log still has something on the box")
	}
}

// An empty log is not a broken log, and saying so is the difference between a
// crawl that has not started and a crawl that has gone wrong.
func TestAnEmptyLogIsNotAFault(t *testing.T) {
	l := Read(nil)
	if !l.Sound() {
		t.Fatalf("an empty log reported faults: %v", l.Faults)
	}
	if !strings.Contains(l.Verdict(), "not that anything is wrong") {
		t.Errorf("an empty log reads as a problem: %s", l.Verdict())
	}
	if l.Bytes() != 0 || l.OnDisk() != 0 {
		t.Error("an empty log has bytes in it")
	}
}

// A reader that stopped at the first fault would be a reader nobody could use
// on the day it mattered, since the rest of the log is still the only record of
// what happened.
func TestEveryFileComesBackEvenWhenSomeOfThemAreWrong(t *testing.T) {
	events := make([]Event, 0, 12)
	events = append(events, without(life("bad-1.warc.gz", 1_000_000_000, Reclaimed), Verified)...)
	events = append(events, life("good-1.warc.gz", 1_000_000_000, Reclaimed)...)
	events = append(events, without(life("bad-2.warc.gz", 1_000_000_000, Reclaimed), Verified)...)

	l := Read(events)
	if len(l.Files) != 3 {
		t.Fatalf("%d files came back out of 3", len(l.Files))
	}
	if len(l.Faults) != 2 {
		t.Errorf("%d faults, want 2: %v", len(l.Faults), l.Faults)
	}
	if l.Reclaimed() != 3_000_000_000 {
		t.Error("the bytes stopped being counted at the first fault")
	}
}

// The states print as words because they end up in a table a person reads.
func TestTheStatesSayWhatTheyAre(t *testing.T) {
	for s, want := range map[State]string{
		Resident: "resident", Pushed: "pushed", Verified: "verified", Reclaimed: "reclaimed",
	} {
		if got := s.String(); got != want {
			t.Errorf("%d printed as %q, want %q", uint8(s), got, want)
		}
	}
	if got := State(9).String(); !strings.Contains(got, "9") {
		t.Errorf("an unknown state printed as %q", got)
	}
}

func without(events []Event, drop State) []Event {
	out := events[:0:0]
	for _, e := range events {
		if e.State != drop {
			out = append(out, e)
		}
	}
	return out
}

// A log line saying the state is 2 is a line that sends somebody to find the
// table that says what 2 is, at the hour when nobody wants to.
func TestALogLineSaysTheStateInWords(t *testing.T) {
	b, err := json.Marshal(life("part-00030.warc.gz", 1_000_000_000, Verified)[2])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"state":"verified"`) {
		t.Errorf("the state was not written as a word: %s", b)
	}

	var got Event
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != Verified {
		t.Errorf("the state came back as %s", got.State)
	}
	if _, err := json.Marshal(State(9)); err == nil {
		t.Error("a state no file can be in was written out anyway")
	}
	if err := json.Unmarshal([]byte(`{"name":"x","state":"deleted"}`), &got); err == nil {
		t.Error("a state that is not one of the four was accepted")
	}
}

func TestALogIsReadFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotation.jsonl")
	var b strings.Builder
	for _, e := range life("part-00040.warc.gz", 1_000_000_000, Reclaimed) {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := ReadLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("%d events read, want 4", len(events))
	}
	if !Read(events).Sound() {
		t.Error("a clean log did not survive the round trip")
	}
}

// A field this reader does not know about means the rotation and the reader
// have drifted apart, which is worth finding out before six weeks of crawl are
// behind it.
func TestALogThisReaderDoesNotUnderstandIsRefused(t *testing.T) {
	for _, tt := range []struct{ name, line string }{
		{"a field nobody here knows", `{"name":"a.warc.gz","state":"pushed","region":"sgp"}`},
		{"an event with no file on it", `{"state":"pushed","bytes":1}`},
		{"a line that is not JSON", `part-00001.warc.gz pushed`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rotation.jsonl")
			if err := os.WriteFile(path, []byte(tt.line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadLog(path); err == nil {
				t.Error("it was read anyway")
			}
		})
	}
	if _, err := ReadLog(filepath.Join(t.TempDir(), "nothing.jsonl")); err == nil {
		t.Error("a log that is not there was read")
	}
}
