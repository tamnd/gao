package may

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// trace builds a watcher's output for a run of the given length, sampled every
// interval, holding hold bytes with one spike near the end.
func trace(box string, ran, every time.Duration, hold, spike int64) []Sample {
	var out []Sample
	seconds, step := int64(ran/time.Second), int64(every/time.Second)
	for s := int64(0); s <= seconds; s += step {
		b := hold
		stage := "download"
		if s > seconds/2 {
			stage = "push"
		}
		if s == seconds-seconds%step {
			b, stage = spike, "push"
		}
		out = append(out, Sample{Second: s, Bytes: b, Box: box, Stage: stage, Workers: 4})
	}
	return out
}

// peakFault is for what is wrong with the run, which exits 2.
func peakFault(t *testing.T, p Peak, want string) {
	t.Helper()
	for _, f := range p.Faults {
		if strings.Contains(f, want) {
			return
		}
	}
	t.Errorf("no fault mentions %q, got:\n  %s", want, strings.Join(p.Faults, "\n  "))
}

// peakRefusal is for what is wrong with the trace, which exits 1. The two are
// kept apart because a trace nobody can read and a run that filled its box call
// for different work, and a caller that gets one code for both cannot tell.
func peakRefusal(t *testing.T, p Peak, want string) {
	t.Helper()
	for _, r := range p.Refused {
		if strings.Contains(r, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, got:\n  %s", want, strings.Join(p.Refused, "\n  "))
}

// The whole point of the item: the gate is a measurement rather than the
// arithmetic, and the arithmetic is still reported next to it.
func TestThePeakIsMeasuredAndTheArithmeticIsReportedBesideIt(t *testing.T) {
	p := Measure("hplt-v3", 6*time.Hour, Ceiling, trace("server1", 6*time.Hour, 20*time.Second, 3_000_000_000, 9_000_000_000))
	if !p.Settled() {
		t.Fatalf("a clean run was faulted: %v", p.Blocking())
	}
	if !p.Passed() {
		t.Errorf("9 GB against a 90 GB ceiling did not pass")
	}
	if p.Held != 9_000_000_000 || p.During != "push" {
		t.Errorf("the peak came back as %d during %s", p.Held, p.During)
	}
	if p.Predicted != PeakBytes(Boxes[3]) {
		t.Errorf("the prediction is %d against %d", p.Predicted, PeakBytes(Boxes[3]))
	}
	if p.Headroom() != Ceiling-p.Held {
		t.Errorf("headroom is %d", p.Headroom())
	}
	for _, want := range []string{"90.0 GB ceiling", "the design predicts", "watched every"} {
		if !strings.Contains(p.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, p.Verdict())
		}
	}
}

// A run can clear the gate and still say the model behind it is wrong, and that
// is worth catching before the fleet changes.
func TestPassingTheCeilingAndMissingTheModelAreDifferentAnswers(t *testing.T) {
	p := Measure("hplt-v3", 6*time.Hour, Ceiling, trace("server1", 6*time.Hour, 20*time.Second, 40_000_000_000, 60_000_000_000))
	if !p.Passed() {
		t.Fatal("60 GB under a 90 GB ceiling did not pass the gate")
	}
	if p.Settled() {
		t.Fatal("a run holding fifteen times what the design predicts came back settled")
	}
	peakFault(t, p, "will surprise somebody on a box with less room")
	if p.Ratio() < 10 {
		t.Errorf("the drift came back as %.1f", p.Ratio())
	}

	// And the other direction, which is a smaller run wearing the gate's name.
	small := Measure("hplt-v3", 6*time.Hour, Ceiling, trace("server1", 6*time.Hour, 20*time.Second, 400_000_000, 900_000_000))
	peakFault(t, small, "a smaller run than the one the ceiling is about")
}

// A peak sampled too rarely is not a peak. A worker can take a shard, write it,
// push it and delete it between two readings five minutes apart.
func TestAPeakSampledTooRarelyIsNotAPeak(t *testing.T) {
	p := Measure("hplt-v3", 6*time.Hour, Ceiling, trace("server1", 6*time.Hour, 5*time.Minute, 3_000_000_000, 9_000_000_000))
	if p.Settled() {
		t.Fatal("a trace sampled every five minutes settled the peak")
	}
	peakRefusal(t, p, "this is the disk at some moments rather than its peak")
	if p.Widest != 5*time.Minute {
		t.Errorf("the widest gap came back as %s", p.Widest)
	}
}

// A run allocates hardest when it starts and when it flushes, which is exactly
// where a watcher that started late or stopped early was not looking.
func TestTheDiskNobodyWatchedIsWhereThePeakWas(t *testing.T) {
	full := trace("server1", 6*time.Hour, 20*time.Second, 3_000_000_000, 9_000_000_000)

	short := Measure("hplt-v3", 6*time.Hour, Ceiling, full[:len(full)/2])
	if short.Settled() {
		t.Fatal("half a run settled the whole one")
	}
	peakRefusal(t, short, "exactly the disk nobody watched")

	late := Measure("hplt-v3", 6*time.Hour, Ceiling, full[20:])
	peakRefusal(t, late, "exactly the disk nobody watched")

	unstated := Measure("hplt-v3", 0, Ceiling, full)
	peakRefusal(t, unstated, "the trace is being taken as the run")
}

func TestAPeakIsAFactAboutOneMachine(t *testing.T) {
	mixed := append(trace("server1", time.Hour, 20*time.Second, 3_000_000_000, 9_000_000_000),
		trace("server3", time.Hour, 20*time.Second, 3_000_000_000, 9_000_000_000)...)
	peakRefusal(t, Measure("hplt-v3", time.Hour, Ceiling, mixed), "rather than about a fleet")

	elsewhere := trace("laptop", time.Hour, 20*time.Second, 3_000_000_000, 9_000_000_000)
	peakRefusal(t, Measure("hplt-v3", time.Hour, Ceiling, elsewhere), "is not a box on the fleet")

	// server2 is the control plane and has less free disk than the ceiling, so a
	// run that stayed under the ceiling there still filled the machine.
	control := trace("server2", time.Hour, 20*time.Second, 3_000_000_000, 6_000_000_000)
	peakFault(t, Measure("hplt-v3", time.Hour, Ceiling, control), "still filled the box")
}

// The S1 ingest ran on server3, which is under the reserve and therefore runs no
// workers, and this is what the first real trace off it exposed.
//
// A box with no workers has no per worker prediction, so the peak has nothing to
// be read against and the drift line has nothing to say. That is not a bad trace
// and it is not a run that failed its gate. It is a run on a box the plan does
// not schedule, and the only place that fact can be reported is here, because
// every other number in the output looks fine: 0.7 GB held against a 90 GB
// ceiling reads as a comfortable pass.
func TestARunOnABoxThePlanGivesNoWorkersIsAFault(t *testing.T) {
	idle := trace("server3", time.Hour, 10*time.Second, 400_000_000, 700_000_000)
	p := Measure("glotcc", time.Hour, Ceiling, idle)

	if len(p.Blocking()) > 0 {
		t.Fatalf("a readable trace was refused: %v", p.Blocking())
	}
	if !p.Passed() {
		t.Error("0.7 GB against a 90 GB ceiling did not pass the gate")
	}
	if p.Settled() {
		t.Error("a run on a box the plan gives no workers came back settled")
	}
	if p.Predicted != 0 || p.Ratio() != 0 {
		t.Errorf("server3 predicts %d bytes at a drift of %.1f, so this test is about a box that no longer exists", p.Predicted, p.Ratio())
	}
	peakFault(t, p, "the arithmetic gives nothing to spend")
}

func TestASampleThatCannotHaveHappenedIsRefused(t *testing.T) {
	base := trace("server1", time.Hour, 20*time.Second, 3_000_000_000, 9_000_000_000)

	noWorkers := append([]Sample(nil), base...)
	noWorkers[3].Workers = 0
	peakRefusal(t, Measure("hplt-v3", time.Hour, Ceiling, noWorkers), "a number per worker rather than a number per box")

	tooBig := append([]Sample(nil), base...)
	tooBig[3].Bytes = 400_000_000_000
	peakRefusal(t, Measure("hplt-v3", time.Hour, Ceiling, tooBig), "this trace is not of server1")

	negative := append([]Sample(nil), base...)
	negative[3].Bytes = -1
	peakRefusal(t, Measure("hplt-v3", time.Hour, Ceiling, negative), "holds -1 bytes")

	empty := Measure("hplt-v3", time.Hour, Ceiling, nil)
	if empty.Passed() || empty.Settled() {
		t.Error("an unwatched run passed")
	}
	peakRefusal(t, empty, "the arithmetic is still the only thing anybody has")
}

// The gate can fail, and when it does the verdict says what it costs rather than
// naming the threshold that was crossed.
func TestARunThatDoesNotFitSaysSo(t *testing.T) {
	p := Measure("hplt-v3", 6*time.Hour, Ceiling, trace("server1", 6*time.Hour, 20*time.Second, 80_000_000_000, 104_000_000_000))
	if p.Passed() {
		t.Fatal("104 GB passed a 90 GB ceiling")
	}
	if p.Headroom() >= 0 {
		t.Errorf("headroom came back as %d", p.Headroom())
	}
	if !strings.Contains(p.Verdict(), "does not fit on the box it was planned for") {
		t.Errorf("the verdict does not say what failing costs: %s", p.Verdict())
	}
}

func TestATraceIsReadFromWhatAWatcherWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.jsonl")
	body := `{"second":0,"bytes":1000000,"box":"server1","stage":"download","workers":4}
{"second":20,"bytes":9000000000,"box":"server1","stage":"push","workers":4}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTrace(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Bytes != 9_000_000_000 {
		t.Errorf("read %+v", got)
	}

	// A field nobody declared is a field the watcher and the reader disagree
	// about, and that is worth failing over rather than ignoring.
	bad := filepath.Join(t.TempDir(), "disk.jsonl")
	if err := os.WriteFile(bad, []byte(`{"second":0,"bytes":1,"box":"server1","workers":1,"gb":9}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrace(bad); err == nil {
		t.Error("a sample with a column nobody declared was read")
	}
	if _, err := ReadTrace(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("a trace that is not there was read")
	}
}
