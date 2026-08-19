package fleet

import (
	"strings"
	"testing"
)

// live is a reading off a box, with the fleet label spelled the way Label
// spells it.
func live(box string, free int64, threads int) Live {
	return Live{Box: box, Path: "/root/gao-ingest", Free: free, Threads: threads}
}

// The measurement that made this exist: server3 was recorded at 44.3 GB on
// 2026-08-03 and had 17.7 GB fifteen days later, and 'gao fleet peak' printed the
// recorded number in a fault sentence about the run. The inventory has been
// retaken since, so the same 26.6 GB is applied to a box that has room to lose
// it, which is the case a run would actually hit next.
func TestDriftCatchesADiskThatFilled(t *testing.T) {
	b, ok := Lookup("server1")
	if !ok {
		t.Fatal("server1 is not on the inventory")
	}
	why := live("server1", b.FreeDisk-26_600_000_000, b.Threads).Drift()
	if len(why) == 0 {
		t.Fatal("26.6 GB of drift read as a box that matches its record")
	}
	if !strings.Contains(why[0], "26.6 GB high") {
		t.Errorf("first sentence is %q, and it should say how far off the record is and in which direction", why[0])
	}
}

// The reserve is the line that decides whether a box may hold corpus bytes, so
// crossing it says more than the number that crossed it.
func TestDriftSaysWhenABoxCrossesTheReserve(t *testing.T) {
	b, _ := Lookup("server1")
	if !HoldsCorpus(b) {
		t.Fatal("server1 is recorded as holding no corpus bytes, and it is the box that fetches")
	}
	why := live("server1", ReserveBytes-1, b.Threads).Drift()
	if len(why) < 2 {
		t.Fatalf("a box under the reserve produced %d sentences, want the drift and the consequence", len(why))
	}
	if !strings.Contains(why[1], "cannot take it") {
		t.Errorf("second sentence is %q, and it should say the plan is handing work to a box that cannot take it", why[1])
	}
}

// The other direction. server2 holds no corpus bytes by rule, and the rule is
// the arithmetic rather than a preference, so the day it has room the plan is
// wrong in the direction nobody checks.
func TestDriftSaysWhenABoxBecomesUsable(t *testing.T) {
	b, ok := Lookup("server2")
	if !ok {
		t.Fatal("server2 is not on the inventory")
	}
	if HoldsCorpus(b) {
		t.Skip("server2 is recorded as holding corpus bytes")
	}
	why := live("server2", ReserveBytes+MinScratchBytes+1, b.Threads).Drift()
	var found bool
	for _, s := range why {
		if strings.Contains(s, "the fleet is larger than the plan thinks") {
			found = true
		}
	}
	if !found {
		t.Errorf("a box that gained room produced %q, and none of it says the fleet grew", why)
	}
}

func TestDriftCatchesAResizedBox(t *testing.T) {
	b, _ := Lookup("server1")
	why := live("server1", b.FreeDisk, b.Threads*2).Drift()
	if len(why) != 1 || !strings.Contains(why[0], "hardware threads") {
		t.Errorf("drift on a box with twice the threads is %q, want one sentence about threads", why)
	}
}

func TestABoxThatMatchesItsRecordHolds(t *testing.T) {
	for _, b := range Boxes {
		l := live(b.Name, b.FreeDisk, b.Threads)
		if !l.Holds() {
			t.Errorf("%s does not match its own record: %q", b.Name, l.Drift())
		}
		if !strings.Contains(l.Verdict(), "matches the inventory") {
			t.Errorf("%s says %q", b.Name, l.Verdict())
		}
	}
}

// A number taken on a laptop is not a fleet number, and the verdict has to say
// so rather than telling somebody to retake an inventory the laptop is not in.
func TestAMachineOffTheFleetSaysSo(t *testing.T) {
	l := live("unmeasured", 35_600_000_000, 10)
	if l.Holds() {
		t.Error("a machine that is not on the inventory passed the check")
	}
	if !strings.Contains(l.Verdict(), "not on the fleet inventory") {
		t.Errorf("verdict is %q", l.Verdict())
	}
}

// Drift is a fact about a filesystem, so the measurement has to come off one.
func TestNowMeasuresARealDirectory(t *testing.T) {
	l, err := Now(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if l.Free <= 0 {
		t.Errorf("measured %d bytes free, and the directory it just wrote a test into exists", l.Free)
	}
	if l.Threads <= 0 {
		t.Errorf("measured %d threads", l.Threads)
	}
	if l.Box != Label() {
		t.Errorf("the reading is labeled %q and this box is %q", l.Box, Label())
	}
}

func TestNowRefusesADirectoryThatIsNotThere(t *testing.T) {
	if _, err := Now(t.TempDir() + "/no-such-directory"); err == nil {
		t.Error("free disk was measured on a directory that does not exist")
	}
}
