package throughput

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// server3 is the box of record for pipeline throughput, which is eight cores
// and 23 GB, and every number in this package is read against those two.
const (
	threads = 8
	memory  = 25_199_222_784
)

// stage is one clean reading: eight workers on server3, a rate somebody could
// plan against, and a worker that stayed inside the ceiling.
func stage(name string, rate float64, rss int64) Stage {
	return Stage{
		Name: name, Box: "server3", Threads: threads, Memory: memory,
		Workers: 8, Docs: int64(rate * 600), Seconds: 600,
		Bytes: int64(rate*600) * 4200, PeakRSS: rss, Solo: rate / 8 / 0.85,
	}
}

// pipeline is the four stages the milestone publishes.
func pipeline() Pipeline {
	return Pipeline{
		Docs: Corpus,
		Stages: []Stage{
			stage("normalize", 1400, 1<<30),
			stage("filter", 900, 1200<<20),
			stage("classify", 210, 2<<30),
			stage("dedup", 620, 1900<<20),
		},
	}
}

func refuses(t *testing.T, p Pipeline, want string) {
	t.Helper()
	for _, why := range p.Blocking() {
		if strings.Contains(why, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(p.Blocking(), "\n  "))
}

func TestThePipelineNamesItsSlowestStageAndTheBoxItRanOn(t *testing.T) {
	p := pipeline()
	if !p.Settled() {
		t.Fatalf("a clean pipeline was refused: %v", p.Blocking())
	}
	if !p.Holds() {
		t.Fatalf("a clean pipeline did not hold: %s", p.Verdict())
	}
	b, _ := p.Bottleneck()
	if b.Name != "classify" {
		t.Errorf("the slowest stage came back as %s at %.0f a second", b.Name, b.Rate())
	}
	if got := b.PerWorker(); got < 26 || got > 27 {
		t.Errorf("eight workers at %.0f a second is %.1f each", b.Rate(), got)
	}
	if got := b.Hours(Corpus); got < 260 || got > 270 {
		t.Errorf("200M documents at %.0f a second came back as %.0f hours", b.Rate(), got)
	}
	for _, want := range []string{"classify is the slowest stage", "on server3", "an estimated 200M documents", "2.5 GB ceiling"} {
		if !strings.Contains(p.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, p.Verdict())
		}
	}

	// A counted corpus and an estimated one are the same arithmetic and not the
	// same claim, so the sentence says which it is.
	counted := pipeline()
	counted.Measured = true
	if !strings.Contains(counted.Verdict(), "a counted 200M documents") {
		t.Errorf("a counted corpus reads as an estimate: %s", counted.Verdict())
	}
}

// The item is the box label, and this is what it is for. The same stage on two
// boxes is two numbers, and neither of them is publishable without saying which.
func TestARateWithoutABoxIsNotARate(t *testing.T) {
	p := pipeline()
	p.Stages[0].Box = ""
	refuses(t, p, "a rate without a box is not a rate")

	elsewhere := pipeline()
	elsewhere.Stages[2].Runs = "gamingpc"
	refuses(t, elsewhere, "an observation rather than the number the plan is built on")

	unknown := pipeline()
	unknown.Stages[1].Threads = 0
	refuses(t, unknown, "checked against the machine it claims to have come off")
}

// A rate is only a rate next to the worker count that produced it, and a run
// with more workers than the box has threads is not throughput.
func TestAWorkerCountIsPartOfTheNumber(t *testing.T) {
	p := pipeline()
	p.Stages[0].Workers = 0
	refuses(t, p, "a throughput over an unknown number of cores")

	over := pipeline()
	over.Stages[0].Workers = 16
	refuses(t, over, "oversubscription reported as throughput")
}

// Peak resident memory per worker is the milestone's own line, and a stage that
// crosses it does not keep its throughput number.
func TestAWorkerOverTheCeilingLosesItsThroughput(t *testing.T) {
	p := pipeline()
	p.Stages[2].PeakRSS = 3 << 30

	if !p.Settled() {
		t.Fatalf("the pipeline was refused on something else: %v", p.Blocking())
	}
	if p.Holds() {
		t.Fatal("a worker holding 3 GB against a 2.5 GB ceiling held")
	}
	if got := len(p.Over()); got != 1 {
		t.Fatalf("%d stages came back over the ceiling", got)
	}
	for _, want := range []string{"3.0 GB in its worst worker", "ceiling of 2.5 GB", "it does not run on server3"} {
		if !strings.Contains(p.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, p.Verdict())
		}
	}

	// Under the ceiling and still too much, because the ceiling is per worker
	// and the box is not. On server3 the two lines never separate, since eight
	// workers at 2.5 GB is 20 of its 23. On server1 they separate immediately,
	// which is the whole reason the box is on the reading.
	small := pipeline()
	small.Stages[0].Box = "server1"
	small.Stages[0].Threads = 4
	small.Stages[0].Memory = 6_213_033_984
	small.Stages[0].Workers = 4
	small.Stages[0].PeakRSS = 2 << 30
	small.Stages[0].Solo = small.Stages[0].Rate() / 4 / 0.85
	if len(small.Over()) != 0 {
		t.Fatal("2.0 GB read as over a 2.5 GB ceiling")
	}
	if small.Holds() {
		t.Fatal("four workers wanting 8.0 GB of a 5.8 GB box held")
	}
	if !strings.Contains(small.Verdict(), "for the page cache every parquet read goes through") {
		t.Errorf("the verdict does not say what the box has left: %s", small.Verdict())
	}
}

// All eight cores busy is a claim about efficiency rather than about top, and
// the reading that separates the two is the single worker rate.
func TestEightCoresBusyIsMeasuredAgainstOne(t *testing.T) {
	p := pipeline()
	p.Stages[1].Solo = p.Stages[1].Rate() / 8 / 0.4
	if e := p.Stages[1].Scaling(); e < 0.39 || e > 0.41 {
		t.Fatalf("a stage at four workers' worth of throughput scaled to %.2f", e)
	}
	refuses(t, p, "busy in the sense that top says they are busy")

	none := pipeline()
	none.Stages[1].Solo = 0
	refuses(t, none, "no efficiency to put next to its rate")

	superlinear := pipeline()
	superlinear.Stages[1].Solo = superlinear.Stages[1].Rate() / 8 / 1.4
	refuses(t, superlinear, "one of the two readings came off a warm cache")
}

// A rate off the first shard is the page cache, and a rate off forty seconds is
// whatever else the box was doing.
func TestARateOffAWarmUpIsNotARate(t *testing.T) {
	short := pipeline()
	short.Stages[3].Docs = 4000
	refuses(t, short, "the first shard and a warm cache")

	quick := pipeline()
	quick.Stages[3].Seconds = 40
	refuses(t, quick, "whatever else the box was doing that minute")

	unmeasured := pipeline()
	unmeasured.Stages[3].PeakRSS = 0
	refuses(t, unmeasured, "per worker rather than per box")
}

// Per stage means the roster, since a table with one row in it is a stage.
func TestPerStageMeansEveryStage(t *testing.T) {
	p := pipeline()
	p.Stages = p.Stages[:2]
	refuses(t, p, "classify, dedup")

	twice := pipeline()
	twice.Stages = append(twice.Stages, twice.Stages[0])
	refuses(t, twice, "two readings of one stage are not two stages")

	nameless := pipeline()
	nameless.Stages[0].Name = ""
	refuses(t, nameless, "cannot be published per stage")

	nodocs := pipeline()
	nodocs.Docs = 0
	refuses(t, nodocs, "every hours figure here is linear in that count")

	empty := Pipeline{Docs: Corpus}
	if empty.Settled() || empty.Holds() {
		t.Error("an empty pipeline settled the throughput item")
	}
	if _, ok := empty.Bottleneck(); ok {
		t.Error("an empty pipeline has a bottleneck")
	}
	if !strings.Contains(empty.Verdict(), "whatever it runs at") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}
}

func TestAPipelineIsReadFromWhatTheBenchmarkAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stages.jsonl")
	body := `{"name":"normalize","box":"server3","threads":8,"memory":25199222784,"workers":8,"docs":840000,"seconds":600,"bytes":3528000000,"peak_rss":1073741824,"solo":205.9}

{"name":"classify","box":"gamingpc","threads":32,"memory":68463005696,"workers":8,"docs":126000,"seconds":600,"bytes":529200000,"peak_rss":2147483648,"solo":30.9}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPipeline(Corpus, false, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Stages) != 2 {
		t.Fatalf("read %d stages", len(p.Stages))
	}
	if b, _ := p.Bottleneck(); b.Name != "classify" || b.Box != "gamingpc" {
		t.Errorf("the bottleneck came back as %+v", b)
	}
	if got := p.Hours(); got < 300 || got > 310 {
		t.Errorf("two stages over 200M documents came back as %.0f hours", got)
	}
	if got := p.Stages[0].Read(); got < 5_800_000 || got > 5_900_000 {
		t.Errorf("normalize read %.0f bytes a second", got)
	}

	// A column nobody declared is the benchmark and the reader disagreeing
	// about what was written down.
	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"name":"normalize","rate":1400}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPipeline(Corpus, false, bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPipeline(Corpus, false, blank); err == nil {
		t.Error("an empty file was read as a pipeline")
	}
	if _, err := ReadPipeline(Corpus, false, filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a pipeline that is not there was read")
	}
}

// The two numbers the ceiling is derived from are server3's, so they are
// checked rather than trusted.
func TestTheCeilingIsServer3sArithmetic(t *testing.T) {
	if got := int64(threads) * Ceiling; got > memory {
		t.Errorf("eight workers at the ceiling want %s of a box with %s", gigabytes(got), gigabytes(memory))
	}
	if left := memory - int64(threads)*Ceiling; left < 3<<30 {
		t.Errorf("eight workers at the ceiling leave %s for everything else", gigabytes(left))
	}
	s := stage("normalize", 1400, Ceiling)
	if s.Over() {
		t.Error("a worker exactly at the ceiling was called over it")
	}
	// The per worker ceiling is the tighter of the two lines on server3, and
	// that is the point of it: nothing has to be recomputed when a stage adds a
	// worker, because eight of them at the ceiling already fit.
	if s.Swaps() {
		t.Errorf("eight workers at the ceiling want %s of %s with %.0f%% reserved",
			gigabytes(s.Resident()), gigabytes(memory), 100*Reserve)
	}
}
