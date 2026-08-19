package harvest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/fleet"
	"github.com/tamnd/gao/law"
)

// The rule the whole manifest exists for. A branch name is a moving target and a
// commit is not, and a corpus pinned to a moving target cannot be rebuilt from
// its own manifest.
func TestNothingIsPinnedToAMovingTarget(t *testing.T) {
	for _, p := range Sources() {
		switch p.Origin {
		case Hub:
			if !commitSHA.MatchString(p.Revision) {
				t.Errorf("%s is pinned to %q", p.Source, p.Revision)
			}
			for _, bad := range []string{"main", "master", "refs/heads/main", "HEAD", "latest"} {
				if p.Revision == bad {
					t.Errorf("%s is pinned to the branch %q", p.Source, bad)
				}
			}
		case Direct:
			if !fileSHA.MatchString(p.Revision) {
				t.Errorf("%s is a direct source pinned to %q, which fixes no file list", p.Source, p.Revision)
			}
		}
	}
}

// The URL is where pinning stops being a property of the manifest and starts
// being a property of the fetch.
func TestAFetchAddressCarriesTheRevision(t *testing.T) {
	for _, p := range Sources() {
		f := p.Files[0]
		got := p.URL(f)
		if !strings.HasSuffix(got, f.Path) {
			t.Errorf("%s: %s does not end in the file path", p.Source, got)
		}
		switch p.Origin {
		case Hub:
			if !strings.Contains(got, "/resolve/"+p.Revision+"/") {
				t.Errorf("%s: %s does not resolve at the pinned commit", p.Source, got)
			}
			if strings.Contains(got, "/resolve/main/") {
				t.Errorf("%s: %s resolves at a branch", p.Source, got)
			}
		case Direct:
			if !strings.HasPrefix(got, p.Repo+"/") {
				t.Errorf("%s: %s is not under the pinned host", p.Source, got)
			}
		}
		if !strings.HasPrefix(got, "https://") {
			t.Errorf("%s: %s is not fetched over TLS", p.Source, got)
		}
	}
}

// HPLT v3 ingests first and alone, because every later source dedups against a
// store that already holds it and the retention numbers in the inventory are
// only reproducible against a fixed reference.
func TestTheSpineIngestsFirst(t *testing.T) {
	srcs := Sources()
	if len(srcs) < 2 {
		t.Fatal("the manifest pins fewer than two sources")
	}
	if srcs[0].Source != doc.SourceHPLT3 {
		t.Errorf("the first source to ingest is %s", srcs[0].Source)
	}
	// Order is dense over the whole manifest rather than over the plan, because
	// dropping a source leaves its order behind rather than renumbering the
	// ones after it.
	for i, p := range AllSources() {
		if p.Order != i {
			t.Errorf("%s is at index %d with order %d, so AllSources does not return ingest order", p.Source, i, p.Order)
		}
	}
	for i := 1; i < len(srcs); i++ {
		if srcs[i].Order <= srcs[i-1].Order {
			t.Errorf("%s at order %d follows %s at order %d, so Sources is not in ingest order",
				srcs[i].Source, srcs[i].Order, srcs[i-1].Source, srcs[i-1].Order)
		}
	}
	if srcs[0].Bytes() < srcs[1].Bytes() {
		t.Errorf("%s ingests first at %s and is smaller than %s at %s, which is not the spine",
			srcs[0].Source, fleet.GB(srcs[0].Bytes()), srcs[1].Source, fleet.GB(srcs[1].Bytes()))
	}
}

// The class in the manifest is a copy of a determination in law, kept here so
// the ingest does not reach across packages mid-stream. A copy that can disagree
// with its original is worse than no copy at all.
func TestEveryPinAgreesWithItsLicenseDetermination(t *testing.T) {
	for _, p := range Sources() {
		ds := law.For(p.Source)
		if len(ds) == 0 {
			t.Errorf("%s is pinned and has no license determination, so it would ingest as unknown", p.Source)
			continue
		}
		var match bool
		for _, d := range ds {
			if d.Class == p.Class {
				match = true
			}
		}
		if !match {
			t.Errorf("%s ingests as %s and luat determines %s", p.Source, p.Class, ds[0].Class)
		}
		if !p.Class.Publishable() {
			t.Errorf("%s ingests as %s, and a public dataset we chose to download should not be one we cannot pass on", p.Source, p.Class)
		}
	}
}

// The six Hugging Face paths in the source enum are the six this milestone
// ingests. A path in the enum with no pin is a path nothing will ever fetch.
func TestEveryPublicCorpusPathIsPinned(t *testing.T) {
	want := []doc.Source{
		doc.SourceHPLT3, doc.SourceFineWeb2, doc.SourceCulturaX,
		doc.SourceFinePDFs, doc.SourceGlotCC, doc.SourceMADLAD400,
	}
	for _, s := range want {
		if _, ok := Pin(s); !ok {
			t.Errorf("%s is an acquisition path with nothing pinned to it", s)
		}
	}
	if got := len(AllSources()); got != len(want) {
		t.Errorf("the manifest pins %d sources and this milestone considers %d", got, len(want))
	}
	if _, ok := Pin(doc.SourceCrawl); ok {
		t.Error("the crawl is pinned in the ingest manifest, and it has no upstream revision to pin to")
	}
}

// An upstream evaluation split ingested into the training corpus is a
// contaminated benchmark, and it is the kind of mistake that is invisible until
// somebody publishes a score.
func TestNoUpstreamEvaluationSplitIsIngested(t *testing.T) {
	for _, p := range Sources() {
		for _, f := range p.Files {
			if strings.Contains(f.Path, "/test/") || strings.Contains(f.Path, "/validation/") {
				t.Errorf("%s ingests %s, which is an upstream holdout", p.Source, f.Path)
			}
		}
	}
	held, ok := Pin(doc.SourceFineWeb2)
	if !ok {
		t.Fatal("FineWeb2 is not pinned")
	}
	if len(held.Excluded) == 0 {
		t.Error("FineWeb2 ships a test split and the manifest holds nothing back")
	}
}

// Held back is not the same as forgotten. Anything the source ships and gao does
// not take is recorded with its size, so that revisiting the decision is reading
// a number rather than re-listing a repo.
func TestWhatIsHeldBackSaysWhyAndHowMuch(t *testing.T) {
	for _, p := range Sources() {
		if len(p.Excluded) == 0 {
			if p.ExcludedBecause != "" {
				t.Errorf("%s explains an exclusion it does not make", p.Source)
			}
			continue
		}
		if p.ExcludedBecause == "" {
			t.Errorf("%s holds back %d files without a reason", p.Source, len(p.Excluded))
		}
		if p.ExcludedBytes() <= 0 {
			t.Errorf("%s holds back files with no size recorded", p.Source)
		}
	}
	m, ok := Pin(doc.SourceMADLAD400)
	if !ok {
		t.Fatal("MADLAD-400 is not pinned")
	}
	if m.ExcludedBytes() < 40_000_000_000 {
		t.Errorf("MADLAD's noisy split records as %s, and it is 53.7 GB", fleet.GB(m.ExcludedBytes()))
	}
}

// The download is the number that decides whether ingestion fits on server1 a
// shard at a time. It is asserted rather than assumed, because it grew by a
// quarter the moment the file lists were read from the hosts.
//
// What is compared against the estimate is everything pinned rather than
// everything fetched. Two sources are pinned and dropped, MADLAD-400 for its
// provenance and CulturaX for a gate nobody here can pass, and their bytes were
// part of the corpus the inventory was estimating. Comparing the fetch list
// against that estimate would read as the manifest having shrunk when what
// happened is that two sources left the run.
func TestTheDownloadIsLargerThanTheInventoryEstimated(t *testing.T) {
	const estimate int64 = 490_000_000_000

	got := TotalBytes() + DroppedBytes()
	if got <= estimate {
		t.Errorf("the pinned download is %s, and the inventory's %s estimate was supposed to be the low one",
			fleet.GB(got), fleet.GB(estimate))
	}
	if got > 2*estimate {
		t.Errorf("the pinned download is %s against a %s estimate, which is too far off to be the same corpus",
			fleet.GB(got), fleet.GB(estimate))
	}
	if DroppedBytes() <= 0 {
		t.Error("nothing reads as dropped, and both MADLAD-400 and CulturaX are")
	}
	if TotalBytes() >= got {
		t.Errorf("the fetch list is %s of a %s manifest, and the dropped sources should be outside it",
			fleet.GB(TotalBytes()), fleet.GB(got))
	}

	// The inventory measured HPLT v3 at 234.5 GB compressed by sampling. The
	// manifest read every shard's size from the host, and the two agreeing is
	// the cheapest available check that the sampling was pointed at the right
	// files.
	h, ok := Pin(doc.SourceHPLT3)
	if !ok {
		t.Fatal("HPLT v3 is not pinned")
	}
	if h.Bytes() < 230_000_000_000 || h.Bytes() > 240_000_000_000 {
		t.Errorf("HPLT v3 pins at %s and the inventory measured 234.5 GB", fleet.GB(h.Bytes()))
	}
	if len(h.Files) != 12 {
		t.Errorf("HPLT v3 pins %d shards and the inventory sampled 12", len(h.Files))
	}
}

// Every file is a resume point, so the count is worth knowing: 154 files is a
// crash costing an average of two gigabytes of re-download, and one file per
// source would be a crash costing forty.
func TestTheIngestHasSomewhereToResumeFrom(t *testing.T) {
	files := Files()
	if files < len(Sources()) {
		t.Fatalf("the manifest pins %d files across %d sources", files, len(Sources()))
	}
	if avg := TotalBytes() / int64(files); avg > 10_000_000_000 {
		t.Errorf("the average pinned file is %s, so an interrupted download loses too much to be worth resuming", fleet.GB(avg))
	}
}

// The reason ingestion streams rather than downloads.
//
// The largest pinned file is one HPLT shard at 26.6 GB, which is more than six
// times server1's entire peak disk budget. An ingest that fetched a file and
// then read it would stop on the first shard of the first source, so it does
// not: it decompresses in flight, projects to the text field, and writes gao
// shards as it goes. That is stated in the plan as a design decision and it is
// here as arithmetic, because the arithmetic is what makes it not optional.
//
// The day this test fails is the day the largest source got small enough to
// download whole, and the streaming path could be reconsidered rather than
// assumed.
func TestTheLargestFileDoesNotFitOnTheBoxThatFetchesIt(t *testing.T) {
	var largest File
	var from doc.Source
	for _, p := range Sources() {
		for _, f := range p.Files {
			if f.Bytes > largest.Bytes {
				largest, from = f, p.Source
			}
		}
	}
	b, ok := fleet.Lookup("server1")
	if !ok {
		t.Fatal("server1 is not in the inventory")
	}
	if largest.Bytes <= fleet.PeakBytes(b) {
		t.Errorf("the largest pinned file is %s in %s at %s, which fits in server1's %s peak, so streaming is now a choice rather than the only option",
			largest.Path, from, fleet.GB(largest.Bytes), fleet.GB(fleet.PeakBytes(b)))
	}
	if largest.Bytes <= fleet.ShardBytes {
		t.Errorf("the largest pinned file is %s and a gao shard is %s, so a source file is no longer larger than what a stage writes",
			fleet.GB(largest.Bytes), fleet.GB(fleet.ShardBytes))
	}
}

func TestEveryPinIsUsable(t *testing.T) {
	for _, p := range Sources() {
		if p.Repo == "" {
			t.Errorf("%s has no repo", p.Source)
		}
		if p.Config == "" {
			t.Errorf("%s names no language partition", p.Source)
		}
		if p.Note == "" {
			t.Errorf("%s does not say why it is in the manifest", p.Source)
		}
		if !strings.HasPrefix(p.Page(), "https://") {
			t.Errorf("%s: %q is not a page anybody can open", p.Source, p.Page())
		}
		for _, f := range p.Files {
			if f.Bytes <= 0 {
				t.Errorf("%s pins %s at %d bytes", p.Source, f.Path, f.Bytes)
			}
			if f.Digest != "" && !fileSHA.MatchString(f.Digest) {
				t.Errorf("%s pins %s with digest %q", p.Source, f.Path, f.Digest)
			}
		}
	}
	if PinnedOn() == "" {
		t.Error("the manifest does not say when it was pinned, so nobody can tell whether it is stale")
	}
}

// Two sources pin no digests and both have a reason a reader can check. HPLT
// publishes none at all. CulturaX is gated, and the Hub masks the digests of a
// gated repo until access is granted. Everywhere else an empty digest means
// somebody left the field out, and that is a file gao would download without
// being able to say it got the right one.
func TestOnlyTheHostsThatWithholdDigestsHaveNone(t *testing.T) {
	for _, p := range Sources() {
		withheld := p.Origin == Direct || p.Gated
		for _, f := range p.Files {
			switch {
			case f.Digest == "" && !withheld:
				t.Errorf("%s pins %s with no digest, and its host publishes one", p.Source, f.Path)
			case f.Digest != "" && withheld:
				t.Errorf("%s pins a digest for %s, and its host does not publish one, so this came from somewhere", p.Source, f.Path)
			}
		}
	}

	h, ok := Pin(doc.SourceHPLT3)
	if !ok {
		t.Fatal("HPLT v3 is not pinned")
	}
	if h.Origin != Direct || h.Gated {
		t.Error("HPLT v3 reads as a gated Hub repo, and it is an open direct download")
	}

	// The gate is the operational fact worth failing on. It is not a permission
	// the code can grant itself, so it has to be visible before an ingest starts
	// rather than at the first 403.
	c, ok := Pin(doc.SourceCulturaX)
	if !ok {
		t.Fatal("CulturaX is not pinned")
	}
	if !c.Gated {
		t.Error("CulturaX reads as ungated, and it requires an accepted agreement before the Hub serves it")
	}
	if !strings.Contains(c.Note, "ated") {
		t.Error("CulturaX is gated and its note does not say so, which is where somebody would look")
	}
	var gated int
	for _, p := range AllSources() {
		if p.Gated {
			gated++
		}
	}
	if gated != 1 {
		t.Errorf("%d sources are gated, and the milestone gate depends on exactly one of them being blockable", gated)
	}
	if !c.Dropped {
		t.Error("CulturaX is gated and not dropped, and the terms were never granted")
	}

	// Nothing gated is on the fetch list. A gated source there is a run that
	// spends its time on a queue and ends in a 403, which is the failure this
	// whole field exists to make visible beforehand.
	for _, p := range Sources() {
		if p.Gated {
			t.Errorf("%s is gated and on the fetch list, so an ingest would run into a 403 on it", p.Source)
		}
	}
}

func TestSourcesHandsOutACopy(t *testing.T) {
	got := Sources()
	got[0].Files[0].Bytes = 1
	got[0].Repo = "somewhere/else"
	if Sources()[0].Files[0].Bytes == 1 {
		t.Error("editing a returned file list edited the manifest")
	}
	if Sources()[0].Repo == "somewhere/else" {
		t.Error("editing a returned source edited the manifest")
	}
	if _, ok := Pin("nothing"); ok {
		t.Error("a source that does not exist is pinned")
	}
}

func TestOriginNamesItself(t *testing.T) {
	if got := fmt.Sprint(Hub); got != "hub" {
		t.Errorf("Hub prints as %q", got)
	}
	if got := fmt.Sprint(Direct); got != "direct" {
		t.Errorf("Direct prints as %q", got)
	}
}

// The loader is what makes the manifest safe to keep as data, so it gets the
// same treatment as any other parser: it is fed things that are not manifests.
func TestTheLoaderRefusesAManifestNobodyCanIngestFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"a version from the future", func(m map[string]any) {
			m["version"] = 2.0
		}, "version"},
		{"no pin date", func(m map[string]any) {
			m["pinned_on"] = ""
		}, "pinned"},
		{"nothing pinned", func(m map[string]any) {
			m["sources"] = []any{}
		}, "pins nothing"},
		{"a source that is not an acquisition path", func(m map[string]any) {
			first(m)["source"] = "hplt4"
		}, "acquisition path"},
		{"a branch instead of a commit", func(m map[string]any) {
			s := source(m, "fineweb2")
			s["revision"] = "main"
		}, "commit SHA"},
		{"a direct source pinned to a commit", func(m map[string]any) {
			s := source(m, "hplt3")
			s["revision"] = "af9c13333eb981300149d5ca60a8e9d659b276b9"
		}, "fixes its file list"},
		{"an origin nobody implements", func(m map[string]any) {
			first(m)["origin"] = "ftp"
		}, "is not an origin"},
		{"no way to detect drift", func(m map[string]any) {
			first(m)["revision_url"] = ""
		}, "drift is undetectable"},
		{"no language partition", func(m map[string]any) {
			first(m)["config"] = ""
		}, "language partition"},
		{"an undetermined license", func(m map[string]any) {
			first(m)["license_class"] = "unknown"
		}, "contract rejects"},
		{"no reason to be there", func(m map[string]any) {
			first(m)["note"] = ""
		}, "why it is in the manifest"},
		{"no files", func(m map[string]any) {
			first(m)["files"] = []any{}
		}, "pins no files"},
		{"a file with no size", func(m map[string]any) {
			files(m)[0].(map[string]any)["bytes"] = 0.0
		}, "0 bytes"},
		{"a file with no path", func(m map[string]any) {
			files(m)[0].(map[string]any)["path"] = ""
		}, "no path"},
		{"a path that climbs out of the repo", func(m map[string]any) {
			files(m)[0].(map[string]any)["path"] = "../../etc/passwd"
		}, "inside the repo"},
		{"the same file twice", func(m map[string]any) {
			fs := files(m)
			fs[1].(map[string]any)["path"] = fs[0].(map[string]any)["path"]
		}, "twice"},
		{"a digest in an algorithm nobody checks", func(m map[string]any) {
			source(m, "fineweb2")["files"].([]any)[0].(map[string]any)["digest"] = "md5:d41d8cd98f00b204e9800998ecf8427e"
		}, "names no algorithm"},
		{"an ungated Hub file with no digest", func(m map[string]any) {
			source(m, "fineweb2")["files"].([]any)[0].(map[string]any)["digest"] = ""
		}, "publishes one for every file"},
		{"two sources at the same point in the order", func(m map[string]any) {
			source(m, "fineweb2")["order"] = source(m, "culturax")["order"]
		}, "ingest order"},
		{"the same source twice", func(m map[string]any) {
			source(m, "fineweb2")["source"] = "culturax"
		}, "pinned twice"},
		{"an exclusion with no reason", func(m map[string]any) {
			source(m, "fineweb2")["excluded_because"] = ""
		}, "without saying why"},
		{"a reason with no exclusion", func(m map[string]any) {
			source(m, "culturax")["excluded_because"] = "no particular reason"
		}, "holds back none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(manifestJSON, &m); err != nil {
				t.Fatalf("the embedded manifest is not JSON: %v", err)
			}
			tc.edit(m)
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("re-encoding the edited manifest: %v", err)
			}
			_, err = load(b)
			if err == nil {
				t.Fatal("the loader accepted it")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the loader said %q, which does not mention %q", err, tc.want)
			}
		})
	}
}

// A field nobody reads is a field somebody thought they were setting.
func TestTheLoaderRefusesAFieldItDoesNotUnderstand(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatalf("the embedded manifest is not JSON: %v", err)
	}
	first(m)["priority"] = "high"
	b, _ := json.Marshal(m)
	if _, err := load(b); err == nil {
		t.Error("the loader accepted a field it does nothing with")
	}
	if _, err := load([]byte("not a manifest")); err == nil {
		t.Error("the loader accepted something that is not JSON")
	}
}

func first(m map[string]any) map[string]any {
	return m["sources"].([]any)[0].(map[string]any)
}

func source(m map[string]any, name string) map[string]any {
	for _, s := range m["sources"].([]any) {
		if s.(map[string]any)["source"] == name {
			return s.(map[string]any)
		}
	}
	panic("no source named " + name)
}

func files(m map[string]any) []any {
	return first(m)["files"].([]any)
}

// A dropped source is pinned and not fetched. It keeps its file list, its byte
// counts and its digests, so re-admitting it is one field rather than a re-pin,
// and the reason it was dropped is in the manifest rather than in a commit
// message nobody reads.
func TestADroppedSourceIsPinnedAndNotInThePlan(t *testing.T) {
	p, ok := Pin(doc.SourceMADLAD400)
	if !ok {
		t.Fatal("madlad400 is not pinned")
	}
	if !p.Dropped {
		t.Fatal("madlad400 is not dropped, and every one of its records fails the provenance rule")
	}
	if len(p.Files) == 0 || p.Bytes() == 0 {
		t.Error("a dropped source lost its file list, so re-admitting it means pinning it again")
	}
	if !strings.Contains(p.DroppedBecause, "text") {
		t.Errorf("the reason does not say what is missing: %q", p.DroppedBecause)
	}

	for _, s := range Sources() {
		if s.Source == doc.SourceMADLAD400 {
			t.Error("a dropped source is in the plan an ingest fetches")
		}
	}
	var found bool
	for _, s := range AllSources() {
		if s.Source == doc.SourceMADLAD400 {
			found = true
		}
	}
	if !found {
		t.Error("a dropped source is not in the manifest a person reads")
	}
}

// The download total is what an ingest moves, and what it does not move is
// printed beside it rather than subtracted quietly.
func TestTheDownloadTotalLeavesOutWhatIsNotFetched(t *testing.T) {
	var plan, dropped int64
	var planFiles int
	for _, p := range AllSources() {
		if p.Dropped {
			dropped += p.Bytes()
			continue
		}
		plan += p.Bytes()
		planFiles += len(p.Files)
	}
	if TotalBytes() != plan {
		t.Errorf("TotalBytes is %d, want %d, the sources that are actually fetched", TotalBytes(), plan)
	}
	if Files() != planFiles {
		t.Errorf("Files is %d, want %d", Files(), planFiles)
	}
	if DroppedBytes() != dropped {
		t.Errorf("DroppedBytes is %d, want %d", DroppedBytes(), dropped)
	}
	if dropped == 0 {
		t.Error("nothing is dropped, so this test proves nothing")
	}
}

func TestAManifestThatDropsASourceWithoutSayingWhyIsRejected(t *testing.T) {
	b := []byte(`{"version":1,"pinned_on":"2026-08-03","sources":[{
	  "source":"glotcc","order":0,"origin":"hub","repo":"cis-lmu/GlotCC-V1",
	  "revision":"9ad140b6be3ac7b539606a2b4809b49d122823de",
	  "revision_url":"https://huggingface.co/api/datasets/cis-lmu/GlotCC-V1",
	  "config":"vie-Latn","gated":false,"license_class":"open","note":"a note",
	  "dropped":true,
	  "files":[{"path":"a.parquet","bytes":1,"digest":"sha256:` + strings.Repeat("a", 64) + `"}]}]}`)
	if _, err := load(b); err == nil {
		t.Error("a source dropped with no reason loaded")
	} else if !strings.Contains(err.Error(), "without saying why") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

func TestAReasonForDroppingASourceThatIsNotDroppedIsRejected(t *testing.T) {
	b := []byte(`{"version":1,"pinned_on":"2026-08-03","sources":[{
	  "source":"glotcc","order":0,"origin":"hub","repo":"cis-lmu/GlotCC-V1",
	  "revision":"9ad140b6be3ac7b539606a2b4809b49d122823de",
	  "revision_url":"https://huggingface.co/api/datasets/cis-lmu/GlotCC-V1",
	  "config":"vie-Latn","gated":false,"license_class":"open","note":"a note",
	  "dropped_because":"a reason for something that did not happen",
	  "files":[{"path":"a.parquet","bytes":1,"digest":"sha256:` + strings.Repeat("a", 64) + `"}]}]}`)
	if _, err := load(b); err == nil {
		t.Error("a reason for dropping a source that is not dropped loaded")
	}
}

// The working snapshot is where an ingest of a source writes, and it carries
// the revision because two revisions of a dataset are two corpora.
func TestASourceNamesTheSnapshotItWritesUnder(t *testing.T) {
	seen := make(map[string]doc.Source)
	for _, p := range AllSources() {
		name := p.Snapshot()
		if !strings.HasPrefix(name, string(p.Source)+"-") {
			t.Errorf("%s writes under %s, which does not name the source", p.Source, name)
		}
		if strings.ContainsAny(name, ":/ ") {
			t.Errorf("%s is not usable as a path partition: %s", p.Source, name)
		}
		if other, ok := seen[name]; ok {
			t.Errorf("%s and %s write under the same snapshot %s", p.Source, other, name)
		}
		seen[name] = p.Source
	}
}

func TestARepinnedSourceWritesSomewhereElse(t *testing.T) {
	p, ok := Pin(doc.SourceGlotCC)
	if !ok {
		t.Fatal("glotcc is not pinned")
	}
	before := p.Snapshot()
	p.Revision = strings.Repeat("a", 40)
	if after := p.Snapshot(); after == before {
		t.Errorf("a re-pinned source still writes under %s, so two revisions land in one directory", after)
	}
}

// The file index is the other half of a staging path, and a path that named a
// file gao does not have would be a path pointing at nothing.
func TestASourceFindsItsOwnFiles(t *testing.T) {
	for _, p := range AllSources() {
		for i, f := range p.Files {
			if got := p.IndexOf(f); got != i {
				t.Errorf("%s puts %s at %d, want %d", p.Source, f.Path, got, i)
			}
		}
		if got := p.IndexOf(File{Path: "no/such/file.parquet"}); got != -1 {
			t.Errorf("%s claims to have a file it does not, at %d", p.Source, got)
		}
	}
}
