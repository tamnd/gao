package pull

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// params is com-8B, whose full training state is 112 GB and whose weights on
// their own are 16.
const params int64 = 8_000_000_000

const state int64 = params * StateBytes

// fleet is the resume the milestone item names: the copy that is still there
// after the host is taken back, pulled over the link the fleet actually has.
func fleet() Resume {
	return Resume{
		Step: 24000, From: "fleet", Source: "server3", Instance: "8xH100",
		Bytes: state, Digest: "b3:9f2c41a7", Verified: "b3:9f2c41a7",
		WroteRanks: 64, ReadRanks: 32,
		Provision: 1080, Pull: 8960, Load: 640, Lost: 3120, Interval: 7200,
		LossAt: 1.8421, LossAfter: 1.8437,
	}
}

// store is the copy a live restart would actually read, on a link that was
// bought for reading corpora and turns out to be the one that matters here.
func store() Resume {
	return Resume{
		Step: 41000, From: "store", Source: "open-index/com-8B-cpt-gao", Instance: "8xH100",
		Bytes: state, Digest: "b3:0d18ee53", Verified: "b3:0d18ee53",
		WroteRanks: 64, ReadRanks: 64,
		Provision: 600, Pull: 448, Load: 640, Lost: 2760, Interval: 7200,
		LossAt: 1.7893, LossAfter: 1.7902,
	}
}

func drill() Drill {
	return Drill{Run: "com-8B-cpt-gao", Params: params, Resumes: []Resume{fleet(), store()}}
}

func refuses(t *testing.T, d Drill, want string) {
	t.Helper()
	for _, why := range d.Blocking() {
		if strings.Contains(why, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(d.Blocking(), "\n  "))
}

func TestARunThatCameBackNamesTheCopyItCameBackFrom(t *testing.T) {
	d := drill()
	if !d.Settled() {
		t.Fatalf("a clean drill was refused: %v", d.Blocking())
	}
	if !d.Holds() {
		t.Fatalf("a clean drill did not hold: %s", d.Verdict())
	}
	f, _ := d.Fastest()
	if f.From != "store" {
		t.Errorf("the cheapest way back in came off the %s", f.From)
	}
	if got := f.Overhead(); got < 0.23 || got > 0.24 {
		t.Errorf("a %s restart against a two hour interval came back as %.0f%%", Duration(f.Restart()), 100*got)
	}
	for _, want := range []string{"came back from the fleet copy at step 24000 intact", "32 ranks reading what 64 wrote", "open-index/com-8B-cpt-gao", "23% of a 2h checkpoint interval"} {
		if !strings.Contains(d.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, d.Verdict())
		}
	}
}

// The finding the drill exists to produce. The fleet copy is correct and it is
// not the copy a live restart can read, because pulling 104 GB over the link
// these boxes have costs more than the interval it is protecting.
func TestTheFleetCopySurvivesTheHostAndCannotServeTheRestart(t *testing.T) {
	d := drill()
	f := d.Fleet()[0]
	if !f.Intact() {
		t.Fatal("the fleet copy did not come back intact")
	}
	if f.Fits() {
		t.Errorf("a %s restart is %.0f%% of a %s interval and was called affordable",
			Duration(f.Restart()), 100*f.Overhead(), Duration(f.Interval))
	}
	if got := len(d.Unaffordable()); got != 1 {
		t.Fatalf("%d resumes came back unaffordable", got)
	}
	if got := f.Rate(); got < 12_000_000 || got > 13_000_000 {
		t.Errorf("104 GB in %s is %.0f bytes a second", Duration(f.Pull), got)
	}
	// Correct and unaffordable at once is the point: the drill still holds,
	// because a second copy answers the cost question the fleet copy cannot.
	if !d.Holds() {
		t.Errorf("a correct fleet resume with an affordable alternative did not hold: %s", d.Verdict())
	}

	// Take the alternative away and the run has nowhere affordable to restart
	// from, which is a real answer rather than a broken drill.
	alone := Drill{Run: "com-8B-cpt-gao", Params: params, Resumes: []Resume{fleet()}}
	if !alone.Settled() {
		t.Fatalf("a single fleet resume was refused: %v", alone.Blocking())
	}
	if alone.Holds() {
		t.Fatal("a run whose only way back in costs 148% of its interval held")
	}
	if !strings.Contains(alone.Verdict(), "restarts more than it trains") {
		t.Errorf("the verdict does not say what an unaffordable restart is: %s", alone.Verdict())
	}
}

// The item's own words. A resume off the training host proves nothing, because
// every path it would have exercised was skipped.
func TestAResumeOffTheTrainingHostIsNotTheTest(t *testing.T) {
	local := fleet()
	local.From = "host"
	local.Source = "local"
	local.Pull = 210
	local.Provision = 0
	d := Drill{Run: "com-8B-cpt-gao", Params: params, Resumes: []Resume{local}}
	refuses(t, d, "the path this item is about has not been run once")

	nowhere := drill()
	nowhere.Resumes[0].From = ""
	refuses(t, nowhere, "the case this item exists to rule out")

	elsewhere := drill()
	elsewhere.Resumes[0].From = "somebody's laptop"
	refuses(t, elsewhere, "not somewhere a checkpoint survives a host being reclaimed")

	// Off the host and still not the fleet copy, which is the one that is there
	// after the reclaim.
	stored := Drill{Run: "com-8B-cpt-gao", Params: params, Resumes: []Resume{store()}}
	refuses(t, stored, "the copy whose resume has to be the tested one")
}

// The failure worth writing a package for: the bytes verified, the loss came
// back higher, and the run recovers before anybody looks again.
func TestTheBytesArrivingIsNotTheStateArriving(t *testing.T) {
	d := drill()
	d.Resumes[0].LossAfter = 1.9032
	refuses(t, d, "keeps the weights and drops the optimizer moments")
	if d.Resumes[0].Matched() != true {
		t.Error("a resume whose digests agree did not match")
	}
	if !d.Resumes[0].Dropped() || d.Resumes[0].Intact() {
		t.Error("a resume that came back 0.06 of loss higher read as intact")
	}

	wrong := drill()
	wrong.Resumes[1].Verified = "b3:44ab0071"
	refuses(t, wrong, "no third copy to arbitrate")

	unchecked := drill()
	unchecked.Resumes[1].Verified = ""
	refuses(t, unchecked, "the one check that a network transfer of 104.3 GB makes necessary")

	unwritten := drill()
	unwritten.Resumes[1].Digest = ""
	refuses(t, unwritten, "was on the host that was reclaimed")

	backwards := drill()
	backwards.Resumes[1].LossAfter = 1.7012
	refuses(t, backwards, "a later checkpoint than the one it says it read")

	oneside := drill()
	oneside.Resumes[1].LossAfter = 0
	refuses(t, oneside, "the loss on only one side of the resume")
}

// A reclaimed host is replaced by whatever capacity was free, so a resume at the
// rank count that wrote the checkpoint has tested the layout that already worked.
func TestAResumeAtTheSameRankCountHasNotResharded(t *testing.T) {
	d := drill()
	d.Resumes[0].ReadRanks = 64
	refuses(t, d, "whatever capacity was free that hour")

	noinstance := drill()
	noinstance.Resumes[0].Instance = ""
	refuses(t, noinstance, "the whole of what makes this a reshard")
}

// What came back has to be a training state. Weights alone load, resume, and
// train from a standing start with none of the moments.
func TestAnExportOfTheWeightsIsNotACheckpoint(t *testing.T) {
	d := drill()
	d.Resumes[1].Bytes = params * Weights
	refuses(t, d, "closer to the weights on their own")

	nosize := drill()
	nosize.Resumes[1].Bytes = 0
	refuses(t, nosize, "nothing to divide into it")

	nomodel := drill()
	nomodel.Params = 0
	refuses(t, nomodel, "a training state or an export of the weights")
}

// A pull that is not a pull, a wait that was not waited, and an interval the
// restart has nothing to be large against.
func TestTheCostHasToBeMeasuredRatherThanAssumed(t *testing.T) {
	quick := drill()
	quick.Resumes[1].Pull = 20
	refuses(t, quick, "a local read rather than a pull")

	instant := drill()
	instant.Resumes[1].Pull = 0
	refuses(t, instant, "arrived from the page cache")

	free := drill()
	free.Resumes[1].Provision = 0
	refuses(t, free, "not replaced the moment it is asked for")

	unloaded := drill()
	unloaded.Resumes[1].Load = 0
	refuses(t, unloaded, "resharding it across a different rank count is not free")

	unscheduled := drill()
	unscheduled.Resumes[1].Interval = 0
	refuses(t, unscheduled, "nothing to be large against")

	twice := drill()
	twice.Resumes = append(twice.Resumes, twice.Resumes[0])
	refuses(t, twice, "two readings of one resume are not two resumes")

	empty := Drill{Run: "com-8B-cpt-gao", Params: params}
	if empty.Settled() || empty.Holds() {
		t.Error("a drill with no resume in it settled the item")
	}
	if _, ok := empty.Worst(); ok {
		t.Error("a drill with no resume has a worst one")
	}
	if _, ok := empty.Fastest(); ok {
		t.Error("a drill with no resume has a cheapest one")
	}
	if !strings.Contains(empty.Verdict(), "a thing somebody believes") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}
}

func TestADrillIsReadFromWhatTheRestartAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resumes.jsonl")
	body := `{"step":24000,"from":"fleet","source":"server3","instance":"8xH100","bytes":112000000000,"digest":"b3:9f2c41a7","verified":"b3:9f2c41a7","wrote_ranks":64,"read_ranks":32,"provision":1080,"pull":8960,"load":640,"lost":3120,"interval":7200,"loss_at":1.8421,"loss_after":1.8437}

{"step":41000,"from":"store","source":"open-index/com-8B-cpt-gao","instance":"8xH100","bytes":112000000000,"digest":"b3:0d18ee53","verified":"b3:0d18ee53","wrote_ranks":64,"read_ranks":64,"provision":600,"pull":448,"load":640,"lost":2760,"interval":7200,"loss_at":1.7893,"loss_after":1.7902}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := ReadDrill("com-8B-cpt-gao", params, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Resumes) != 2 || !d.Holds() {
		t.Fatalf("read %d resumes, holds %v: %s", len(d.Resumes), d.Holds(), d.Verdict())
	}
	if w, _ := d.Worst(); w.From != "fleet" {
		t.Errorf("the most expensive restart came off the %s", w.From)
	}
	if got := d.State(); got != 112_000_000_000 {
		t.Errorf("an 8B training state came back as %s", gigabytes(got))
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"step":1,"minutes":40}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDrill("com-8B-cpt-gao", params, bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDrill("com-8B-cpt-gao", params, blank); err == nil {
		t.Error("an empty file was read as a drill")
	}
	if _, err := ReadDrill("com-8B-cpt-gao", params, filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a drill that is not there was read")
	}
}

func TestADigestIsComparedAtTheLengthAPersonReads(t *testing.T) {
	if got := short("b3:9f2c41a7d0"); got != "b3:9f2c41a7" {
		t.Errorf("short is %q", got)
	}
	if got := short("9f2c41a7d0"); got != "9f2c41a7" {
		t.Errorf("short with no prefix is %q", got)
	}
	if got := short("b3:9f"); got != "b3:9f" {
		t.Errorf("a digest shorter than the window is %q", got)
	}
}
