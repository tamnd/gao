package may

import (
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
)

func TestEveryBoxIsCompletelyDescribed(t *testing.T) {
	seen := make(map[string]bool)
	for _, b := range Boxes {
		if seen[b.Name] {
			t.Errorf("%s appears twice in the inventory", b.Name)
		}
		seen[b.Name] = true

		if b.OS == "" || b.Arch == "" || b.CPU == "" {
			t.Errorf("%s is missing its platform", b.Name)
		}
		if b.Hostname == "" {
			t.Errorf("%s has no hostname recorded, so a run on it labels itself unmeasured", b.Name)
		}
		if b.Cores <= 0 || b.Threads < b.Cores {
			t.Errorf("%s has %d cores and %d threads", b.Name, b.Cores, b.Threads)
		}
		if b.Memory <= 0 || b.Disk <= 0 {
			t.Errorf("%s has no memory or no disk", b.Name)
		}
		if b.FreeDisk > b.Disk {
			t.Errorf("%s has %s free of %s", b.Name, GB(b.FreeDisk), GB(b.Disk))
		}
		if b.Role == "" {
			t.Errorf("%s has no role, which is how a box ends up running the wrong stage", b.Name)
		}
		if b.HasGPU() && b.GPUMemory <= 0 {
			t.Errorf("%s has a GPU with no memory recorded, and batch size is a function of that number", b.Name)
		}
		if !b.HasGPU() && b.GPUMemory != 0 {
			t.Errorf("%s has GPU memory and no GPU", b.Name)
		}
	}
}

func TestTotalSumsTheInventory(t *testing.T) {
	got := Total()
	if got.Boxes != len(Boxes) {
		t.Errorf("Total counted %d boxes, the inventory has %d", got.Boxes, len(Boxes))
	}
	var cores int
	var free int64
	for _, b := range Boxes {
		cores += b.Cores
		free += b.FreeDisk
	}
	if got.Cores != cores || got.FreeDisk != free {
		t.Errorf("Total gave %d cores and %s free, want %d and %s", got.Cores, GB(got.FreeDisk), cores, GB(free))
	}
}

func TestThereIsExactlyOneGPU(t *testing.T) {
	// The whole shape of S2, S4, S5, and S9 follows from this. If it ever stops
	// being one, the plans that assume a single accelerator need rereading, and
	// this is the cheapest place to be told.
	if got := Total().GPUs; got != 1 {
		t.Errorf("the fleet has %d GPUs, the plan is written for 1", got)
	}
	b := Largest()
	if !b.HasGPU() {
		t.Logf("the GPU box is not the box with the most free disk, which is worth knowing when placing a stage")
	}
}

func TestTheCorpusDoesNotFitOnTheFleet(t *testing.T) {
	// This is the constraint every slice is written against, so it is asserted
	// rather than assumed. It is also written to fail if the fleet ever grows
	// enough for the corpus to fit, because that is the moment the streaming
	// design stops being forced and starts being a choice.
	p := Plan(TargetTokens)
	if p.Resident {
		t.Fatalf("the compressed corpus is %s and %s now has %s free, so the plan's streaming constraint no longer follows from the hardware",
			GB(p.Compressed), p.Largest.Name, GB(p.Largest.FreeDisk))
	}
	// The fleet total is the encouraging number and it is the wrong one. A stage
	// reads a shard and writes a shard, so processing a corpus in place needs
	// room for about two of it, and the fleet does not have that even summed
	// across four machines with nothing else on them.
	if p.FleetFree >= 2*p.Compressed {
		t.Errorf("the fleet has %s free against a %s corpus, which is enough headroom to process it in place, so the off-box store is now a choice rather than a constraint",
			GB(p.FleetFree), GB(p.Compressed))
	}
	// It was 774 shards while the compression ratio was assumed at 3.0, 1121 once
	// the ratio was the measured 2.07, and 1207 once characters per token was the
	// measured 3.28 rather than the assumed 3.0. The band is around the
	// measurements rather than around the number the release was first sized for,
	// since moving the band to keep the old shard count is how a measurement
	// gets negotiated away.
	if p.Shards < 1000 || p.Shards > 1350 {
		t.Errorf("the budget comes to %d shards, the release format is written for around 1200", p.Shards)
	}
	if p.ShardsResident < 10 {
		t.Errorf("only %d shards fit on %s at once, which is too small a working set to run a stage against", p.ShardsResident, p.Largest.Name)
	}
}

func TestPlanAgreesWithTheUnitConversions(t *testing.T) {
	// The budget is only as good as the constants behind it, so this pins the
	// link rather than trusting it.
	tokensPerGB := float64(doc.TokensPerGB)
	p := Plan(int64(tokensPerGB))
	if want := int64(doc.BytesPerGB); p.Text < want*9/10 || p.Text > want*11/10 {
		t.Errorf("one gigabyte of tokens came to %s of text, want about %s", GB(p.Text), GB(want))
	}
}

func TestHoldsAnswersAgainstTheLargestBox(t *testing.T) {
	big := Largest()
	if b, ok := Holds(big.FreeDisk); !ok || b.Name != big.Name {
		t.Errorf("Holds(%s) gave %q, %v, want %s", GB(big.FreeDisk), b.Name, ok, big.Name)
	}
	if _, ok := Holds(big.FreeDisk + 1); ok {
		t.Error("Holds accepted one byte more than the largest box has free")
	}
}

func TestLookupTakesEitherNameAndIgnoresCase(t *testing.T) {
	if b, ok := Lookup("server3"); !ok || b.Cores != 8 {
		t.Errorf("Lookup(server3) gave %+v, %v", b, ok)
	}
	// The machines do not call themselves what we call them, and a run on a real
	// box has to label itself correctly with nothing set in the environment.
	for _, b := range Boxes {
		got, ok := Lookup(b.Hostname)
		if !ok || got.Name != b.Name {
			t.Errorf("Lookup(%q) gave %q, %v, want %s", b.Hostname, got.Name, ok, b.Name)
		}
	}
	if b, ok := Lookup("GAMINGPC"); !ok || b.Name != "gamingpc" {
		t.Errorf("Lookup is case sensitive, which breaks the Windows box")
	}
	if _, ok := Lookup("laptop"); ok {
		t.Error("Lookup found a box that is not on the fleet")
	}
}

func TestLabelPrefersTheEnvironmentAndFallsBackToUnmeasured(t *testing.T) {
	t.Setenv(BoxEnv, "server1")
	if got := Label(); got != "server1" {
		t.Errorf("Label is %q with %s=server1", got, BoxEnv)
	}
	if b, ok := Current(); !ok || b.Name != "server1" {
		t.Errorf("Current is %+v, %v", b, ok)
	}

	// A hostname that is not on the fleet has to read as unmeasured, because the
	// failure this guards against is a laptop number quietly passing for a box
	// of record number.
	t.Setenv(BoxEnv, "some-rented-instance")
	if got := Label(); got != "unmeasured" {
		t.Errorf("Label is %q for a machine that is not on the fleet, want unmeasured", got)
	}
	if _, ok := Current(); ok {
		t.Error("Current claimed a machine that is not on the fleet")
	}

	// A fully qualified name still names the box.
	t.Setenv(BoxEnv, "  gamingpc  ")
	if got := Label(); got != "gamingpc" {
		t.Errorf("Label is %q for a padded label, want gamingpc", got)
	}
}

func TestGB(t *testing.T) {
	if got := GB(1_500_000_000); got != "1.5 GB" {
		t.Errorf("GB(1.5e9) is %q", got)
	}
	if !strings.HasSuffix(GB(0), "GB") {
		t.Errorf("GB(0) is %q", GB(0))
	}
}

func TestSizePicksAReadableUnit(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1_000, "1.0 kB"},
		{512_000, "512.0 kB"},
		{1_000_000, "1.0 MB"},
		{512_000_000, "512.0 MB"},
		{1_000_000_000, "1.0 GB"},
		{ShardBytes * 750, "384.0 GB"},
	}
	for _, tc := range cases {
		if got := Size(tc.in); got != tc.want {
			t.Errorf("Size(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
