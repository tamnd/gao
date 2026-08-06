package don

import (
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/may"
)

// The plan the crawl is actually written against has to work, and it has to
// work on the box named in the milestone rather than on a box with a
// convenient amount of disk.
func TestTheCrawlAsPlannedKeepsUpWithItself(t *testing.T) {
	r := Target()
	if r.Box.Name != "server1" {
		t.Fatalf("the rotation is planned for %s, and the crawl runs on server1", r.Box.Name)
	}
	if !r.Fits() {
		t.Fatalf("the planned rotation does not work: %v", r.Blocking())
	}
	if !r.Keeps() {
		t.Error("the uplink does not keep up with the crawl, which makes every other number here a countdown")
	}
	if r.Held() >= r.Mark() {
		t.Errorf("steady state holds %s against a %s mark", may.Size(r.Held()), may.Size(r.Mark()))
	}
}

// An uplink slower than the crawl is not a tuning problem. It is a deadline,
// and the number that matters is when rather than by how much.
func TestAnUplinkThatCannotKeepUpGetsADeadlineRatherThanAWarning(t *testing.T) {
	r := Target()
	r.Uplink = int64(r.Fill()) / 4

	if r.Keeps() {
		t.Fatal("an uplink at a quarter of the fill rate was reported as keeping up")
	}
	full, ok := r.Full()
	if !ok {
		t.Fatal("no deadline was given for a disk that fills")
	}
	if full <= 0 || full > 24*time.Hour {
		t.Errorf("the disk fills in %s, which is not a number anybody would act on", span(full))
	}
	if r.Fits() {
		t.Error("a rotation that fills the disk was reported as fitting")
	}
	if !strings.Contains(strings.Join(r.Blocking(), " "), "no cleanup pass recovers that") {
		t.Errorf("the reason does not say why more cleanup is not the answer: %v", r.Blocking())
	}
}

// A rotation that keeps up on rate can still not fit, and this is the case
// people leave out of capacity plans.
func TestKeepingUpOnRateIsNotTheSameAsFitting(t *testing.T) {
	r := Target()
	r.Confirm = 12 * time.Hour

	if !r.Keeps() {
		t.Fatal("changing the confirmation lag changed whether the uplink keeps up")
	}
	if r.Fits() {
		t.Fatalf("a twelve hour confirmation lag still fit in %s", may.Size(r.Mark()))
	}
	if !strings.Contains(strings.Join(r.Blocking(), " "), "nothing may be deleted in between") {
		t.Errorf("the reason does not name the window: %v", r.Blocking())
	}
}

// The open file is not pushable, so a volume larger than the disk is a rotation
// that dies before it can do anything at all.
func TestAVolumeBiggerThanTheDiskIsRefused(t *testing.T) {
	r := Target()
	r.Volume = r.Scratch() * 2

	if r.Fits() {
		t.Fatal("a volume twice the size of the disk fit on it")
	}
	if !strings.Contains(strings.Join(r.Blocking(), " "), "before the first file is even closed") {
		t.Errorf("the reason does not say what happens: %v", r.Blocking())
	}
}

// A box with no route off it is the whole crawl in one disk, and saying so is
// more useful than reporting a fill rate.
func TestABoxWithNoRouteOffItSaysSo(t *testing.T) {
	r := Target()
	r.Uplink = 0

	if r.Fits() {
		t.Fatal("a box with no uplink was reported as fitting")
	}
	if !strings.Contains(strings.Join(r.Blocking(), " "), "no route off the box") {
		t.Errorf("the reason does not name the problem: %v", r.Blocking())
	}
	if _, ok := r.Full(); !ok {
		t.Error("a box with no uplink was not given a deadline")
	}
}

// Two things wrong at once is the ordinary case, and fixing the first then
// discovering the second is how an afternoon goes.
func TestEveryReasonComesBackRatherThanTheFirst(t *testing.T) {
	r := Target()
	r.Uplink = 1
	r.Volume = r.Scratch() * 2

	if got := len(r.Blocking()); got < 2 {
		t.Fatalf("%d reasons for a rotation that is wrong two ways: %v", got, r.Blocking())
	}
}

// This is the sentence in the milestone about a hundred gigabytes being a few
// hours of fetching, computed rather than asserted.
func TestHowLongTheStoreCanBeUnreachable(t *testing.T) {
	r := Target()
	out := r.Outage()
	if out < time.Hour || out > 24*time.Hour {
		t.Errorf("the store can be unreachable for %s, which is not the order of magnitude the plan is written against", span(out))
	}
	if !strings.Contains(r.Verdict(), "before fetching has to stop") {
		t.Errorf("the verdict does not say what happens at the mark: %s", r.Verdict())
	}
}

// The mark is below the disk on purpose, because the pause has to leave room
// for what is already in flight to land.
func TestTheCrawlStopsBeforeTheDiskIsFull(t *testing.T) {
	r := Target()
	if r.Mark() >= r.Scratch() {
		t.Errorf("the mark is %s and scratch is %s, so there is no room for anything in flight",
			may.Size(r.Mark()), may.Size(r.Scratch()))
	}
	if r.Scratch() >= r.Box.FreeDisk {
		t.Error("scratch does not leave the reserve that keeps the machine loggable into")
	}
}

// An idle box is a description rather than a plan, and dividing by its fill
// rate is the obvious way for this package to panic in production.
func TestARotationThatWritesNothingIsNotAPlan(t *testing.T) {
	var r Rotation
	if r.Fits() {
		t.Fatal("an empty rotation fit")
	}
	if r.Held() != 0 || r.Flight() != 0 || r.Rotate() != 0 || r.Outage() != 0 {
		t.Error("an idle box has numbers on it")
	}
	if !strings.Contains(strings.Join(r.Blocking(), " "), "idle box") {
		t.Errorf("the reason does not say what it is looking at: %v", r.Blocking())
	}
}

// A file half uploaded still occupies all of itself, so the backlog is counted
// in whole volumes.
func TestTheBacklogIsCountedInWholeFiles(t *testing.T) {
	r := Target()
	if got := r.Held() % r.Volume; got != 0 {
		t.Errorf("steady state holds %s, which is not a whole number of volumes", may.Size(r.Held()))
	}
	if r.Held() <= r.Volume {
		t.Error("steady state counts only the open file, which assumes a push is instant")
	}
}

// A capacity plan nobody can read is a capacity plan nobody checks.
func TestDurationsAreWrittenForPeople(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30 seconds"},
		{45 * time.Minute, "45 minutes"},
		{6 * time.Hour, "6.0 hours"},
		{5 * 24 * time.Hour, "5.0 days"},
	} {
		if got := span(tt.d); got != tt.want {
			t.Errorf("%s printed as %q, want %q", tt.d, got, tt.want)
		}
	}
}
