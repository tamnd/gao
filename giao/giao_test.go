package giao

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/gat"
)

// reading writes one plausible reading, so that a test which cares about the
// split does not have to restate the four fields that make a reading valid.
func reading(box string, mbPerSec float64) Reading {
	const seconds = 3600.0
	return Reading{
		Box:     box,
		Bytes:   int64(mbPerSec * 1e6 * seconds),
		Seconds: seconds,
		On:      "2026-08-03",
		How:     "an hour of the hplt3 ingest, off the run ledger",
	}
}

func fleet() []Reading {
	return []Reading{reading("server1", 60), reading("server3", 40), reading("gamingpc", 25)}
}

func TestTheSplitHandsOutEveryFileTheManifestPins(t *testing.T) {
	s := Divide(fleet())

	if s.Files != gat.Files() {
		t.Errorf("the split covers %d files, the manifest pins %d", s.Files, gat.Files())
	}
	if s.Bytes != gat.TotalBytes() {
		t.Errorf("the split covers %d bytes, the manifest pins %d", s.Bytes, gat.TotalBytes())
	}

	if len(s.Unplaced) > 0 {
		t.Errorf("%d files were left on the floor: %+v", len(s.Unplaced), s.Unplaced)
	}

	var files int
	var bytes int64
	seen := map[string]bool{}
	for _, g := range s.Group {
		for _, h := range g.Hands {
			for _, j := range h.Jobs {
				key := j.Source + "/" + j.Path
				if seen[key] {
					t.Errorf("%s is handed out twice", key)
				}
				seen[key] = true
				files++
				bytes += j.Bytes
			}
		}
	}
	if files != s.Files || bytes != s.Bytes {
		t.Errorf("the hands hold %d files and %d bytes, the manifest asks for %d and %d", files, bytes, s.Files, s.Bytes)
	}
}

func TestAGroupIsOneIngestOrderAndNothingElse(t *testing.T) {
	s := Divide(fleet())

	if len(s.Group) < 2 {
		t.Fatalf("the ingest divides into %d groups, which is not a sequence", len(s.Group))
	}
	for i, g := range s.Group {
		if i > 0 && g.Order <= s.Group[i-1].Order {
			t.Errorf("group %d is order %d, after order %d", i, g.Order, s.Group[i-1].Order)
		}
		for _, h := range g.Hands {
			for _, j := range h.Jobs {
				if j.Order != g.Order {
					t.Errorf("%s is order %d and was handed out in group %d", j.Path, j.Order, g.Order)
				}
			}
		}
	}

	// HPLT v3 is pinned first and ingests alone, because every later source
	// dedups against a store that already holds it.
	first := s.Group[0]
	if len(first.Sources) != 1 || first.Sources[0] != "hplt3" {
		t.Errorf("the first group is %v, the manifest pins hplt3 to ingest alone", first.Sources)
	}
}

func TestTheWholeIngestIsTheGroupsEndToEnd(t *testing.T) {
	s := Divide(fleet())

	var want float64
	for _, g := range s.Group {
		want += g.Makespan()
	}
	if got := s.Seconds(); got != want {
		t.Errorf("the ingest takes %.0f seconds, its groups take %.0f end to end", got, want)
	}
	if s.Seconds() < s.Perfect() {
		t.Errorf("the schedule takes %.0f seconds against a floor of %.0f, which no arrangement can be under", s.Seconds(), s.Perfect())
	}
	if s.Alone() <= s.Seconds() {
		t.Errorf("three boxes take %.0f seconds and the fastest alone takes %.0f, so the fleet bought nothing", s.Seconds(), s.Alone())
	}
}

func TestTheHeaviestFileGoesToWhicheverBoxWouldHaveItSoonest(t *testing.T) {
	s := Divide(fleet())

	// The fastest box draws the most bytes. It is not a rule the split enforces,
	// it is what the rule produces, so it is worth reading back.
	fast, slow := s.BytesFor("server1"), s.BytesFor("gamingpc")
	if fast <= slow {
		t.Errorf("server1 draws %d bytes at 60 MB/s and gamingpc draws %d at 25 MB/s", fast, slow)
	}
}

func TestABoxIsNotOfferedAFileItCannotLand(t *testing.T) {
	// server3 has 24.3 GB of scratch and holds 8.2 GB of stage working set while
	// it fetches, and the largest file the manifest pins is 26.6 GB. Make it the
	// fastest box on the fleet and it still does not draw that file.
	s := Divide([]Reading{reading("server3", 200), reading("server1", 50), reading("gamingpc", 40)})

	room := Room("server3")
	if room <= 0 || room >= 26_000_000_000 {
		t.Fatalf("server3 has %d bytes of room, which is not the fleet this test is about", room)
	}
	for _, g := range s.Group {
		for _, h := range g.Hands {
			for _, j := range h.Jobs {
				if j.Bytes > Room(h.Box) {
					t.Errorf("%s draws %s at %d bytes into %d bytes of room", h.Box, j.Path, j.Bytes, Room(h.Box))
				}
			}
		}
	}
	if len(s.Unplaced) > 0 {
		t.Errorf("gamingpc has 276 GB of room and %d files were left on the floor anyway", len(s.Unplaced))
	}
	// It is still the fastest box, so it still draws the most bytes.
	if s.BytesFor("server3") <= s.BytesFor("server1") {
		t.Errorf("server3 draws %d bytes at 200 MB/s and server1 draws %d at 50 MB/s", s.BytesFor("server3"), s.BytesFor("server1"))
	}
}

func TestABoxThatMayNotHoldCorpusBytesDrawsNothing(t *testing.T) {
	s := Divide(append(fleet(), reading("server2", 50)))

	if len(s.Idle) != 1 || s.Idle[0] != "server2" {
		t.Fatalf("the split reports %v idle, server2 has eight gigabytes of scratch and may not hold corpus bytes", s.Idle)
	}
	if n := s.BytesFor("server2"); n != 0 {
		t.Errorf("server2 draws %d bytes", n)
	}
	for _, box := range s.Boxes() {
		if box == "server2" {
			t.Error("server2 is listed among the boxes drawing work")
		}
	}
	// Reported, not silently dropped. Somebody measured that box and is owed an
	// answer about why it is not in the schedule.
	if !strings.Contains(strings.Join(s.Idle, " "), "server2") {
		t.Error("the reading for server2 disappears without a word")
	}
}

func TestASplitWithNowhereToLandIsNotASchedule(t *testing.T) {
	cases := []struct {
		name     string
		readings []Reading
		says     string
	}{
		{"no readings at all", nil, "no readings were given"},
		{"a box nobody has", []Reading{reading("server9", 60)}, "not on the fleet inventory"},
		{"only a box that cannot hold corpus bytes", []Reading{reading("server2", 60)}, "nowhere for the ingest to land"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Divide(c.readings)
			why := s.Blocking()
			if len(why) == 0 {
				t.Fatalf("%s reads as a schedule", c.name)
			}
			if !strings.Contains(strings.Join(why, " "), c.says) {
				t.Errorf("the refusal is %q, it does not say %q", why, c.says)
			}
			if s.Holds() {
				t.Error("a split that is not a schedule holds")
			}
			if s.Faults() != nil || s.Waiting() != nil {
				t.Error("a split that is not a schedule still reports faults and waiting under the refusal")
			}
			if !strings.HasPrefix(s.Verdict(), "This is not a schedule") {
				t.Errorf("the verdict is %q", s.Verdict())
			}
		})
	}
}

func TestAReadingThatIsNotAMeasurementIsRefused(t *testing.T) {
	short := reading("server1", 60)
	short.Bytes = MinSample / 2
	short.Seconds = 4

	stopped := reading("server1", 60)
	stopped.Seconds = 0

	silent := reading("server1", 60)
	silent.How = ""

	cases := []struct {
		name string
		l    Reading
		says string
	}{
		{"taken over too little", short, "rather than of a run"},
		{"taken over no time", stopped, "in 0 seconds"},
		{"not saying how", silent, "does not say how it was taken"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			why := Divide([]Reading{c.l, reading("server3", 40)}).Blocking()
			if !strings.Contains(strings.Join(why, " "), c.says) {
				t.Errorf("the refusal is %q, it does not say %q", why, c.says)
			}
		})
	}
}

func TestTwoRatesForOneBoxAreAQuestionRatherThanAMeasurement(t *testing.T) {
	first := reading("server1", 60)
	second := reading("SERVER1", 20)
	second.On = "2026-08-04"

	why := Divide([]Reading{first, second}).Blocking()
	joined := strings.Join(why, " ")
	if !strings.Contains(joined, "two readings") {
		t.Fatalf("the refusal is %q", why)
	}
	// Both dates, because which of the two to keep is the reader's call and they
	// cannot make it without knowing when each was taken.
	for _, date := range []string{"2026-08-03", "2026-08-04"} {
		if !strings.Contains(joined, date) {
			t.Errorf("the refusal does not say %s: %q", date, joined)
		}
	}
}

func TestAGroupWithTooFewFilesToDivideSaysSoWithoutCallingItAFault(t *testing.T) {
	s := Divide(fleet())

	if len(s.Blocking()) > 0 {
		t.Fatalf("the fleet is refused a schedule: %q", s.Blocking())
	}
	waiting := s.Waiting()
	if len(waiting) == 0 {
		t.Fatal("finepdfs pins three files across three boxes of different speeds and nothing says the group ends late")
	}
	// finepdfs is three files, so on three boxes of different speeds it ends when
	// the slowest of them ends however the three are dealt out.
	if !strings.Contains(strings.Join(waiting, "\n"), "order 1 divides 3 files across 3 boxes") {
		t.Errorf("the groups reported as ending late are %q, and finepdfs is not among them", waiting)
	}
	for _, w := range waiting {
		if !strings.Contains(w, "cannot be handed to a second box") {
			t.Errorf("%q does not say why a better split would not help", w)
		}
	}
	// A group ending late is the manifest's shape, so it does not stop the run.
	if !s.Holds() {
		t.Errorf("a group ending late stops the run: %q", s.Faults())
	}
}

func TestABoxThatDrawsNothingIsAFaultRatherThanASchedule(t *testing.T) {
	// One box six hundred times slower than the others is never the box that
	// would finish a file soonest, so it draws nothing at all and the reading
	// somebody took on it bought nothing.
	s := Divide([]Reading{reading("server1", 600), reading("gamingpc", 600), reading("server3", 1)})

	faults := s.Faults()
	if len(faults) != 1 {
		t.Fatalf("server3 draws %d bytes and the faults are %q", s.BytesFor("server3"), faults)
	}
	if !strings.HasPrefix(faults[0], "server3 draws no files") {
		t.Errorf("the fault is %q", faults[0])
	}
	if s.Holds() {
		t.Error("a schedule with a fault holds")
	}
	if !strings.Contains(s.Verdict(), "not the schedule to run") {
		t.Errorf("the verdict is %q", s.Verdict())
	}
}

func TestTheVerdictQuotesTheScheduleAgainstTheOneBoxItReplaces(t *testing.T) {
	v := Divide(fleet()).Verdict()

	for _, want := range []string{"122 files", "3 boxes", "fastest box alone", "no arrangement can beat"} {
		if !strings.Contains(v, want) {
			t.Errorf("the verdict does not say %q: %q", want, v)
		}
	}
	if strings.Contains(v, "  ") || strings.Contains(v, "\n") {
		t.Errorf("the verdict is not one sentence a reader can quote: %q", v)
	}
}

func TestJobsAreHandedOutHeaviestFirstWithinAnOrder(t *testing.T) {
	jobs := Jobs()
	if len(jobs) != gat.Files() {
		t.Fatalf("Jobs covers %d files, the manifest pins %d", len(jobs), gat.Files())
	}
	for i := 1; i < len(jobs); i++ {
		prev, this := jobs[i-1], jobs[i]
		switch {
		case this.Order < prev.Order:
			t.Fatalf("%s is order %d, after order %d", this.Path, this.Order, prev.Order)
		case this.Order == prev.Order && this.Bytes > prev.Bytes:
			t.Fatalf("%s is %d bytes and comes after %s at %d", this.Path, this.Bytes, prev.Path, prev.Bytes)
		}
	}
	// A dropped source is not work anybody has to do.
	for _, j := range jobs {
		if j.Source == "madlad400" {
			t.Errorf("%s is handed out and the manifest drops madlad400", j.Path)
		}
	}
}

func TestARateIsTheTwoNumbersRatherThanTheirQuotient(t *testing.T) {
	if got := (Reading{Bytes: 6e9, Seconds: 100}).Rate(); got != 6e7 {
		t.Errorf("6 GB in 100 seconds reads as %.0f bytes per second", got)
	}
	if got := (Reading{Bytes: 6e9, Seconds: 0}).Rate(); got != 0 {
		t.Errorf("a reading over no time reads as %.0f rather than nothing", got)
	}
}
