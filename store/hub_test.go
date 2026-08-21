package store

import (
	"fmt"
	"path"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/law"
)

// The test the whole table exists for. Every repo here is public, so a repo that
// carries text may only carry text the publication posture says ships, and this
// is what makes that a property of the code rather than of whoever creates the
// repo.
func TestNoRepoCarriesTextItMayNotPublish(t *testing.T) {
	for _, d := range Datasets() {
		if !d.Text {
			continue
		}
		for _, c := range d.Classes {
			if !law.Publishes(c).Text {
				t.Errorf("%s carries text and admits %s, whose text does not ship", d.Repo(), c)
			}
			if !d.Admits(c) {
				t.Errorf("%s names %s and then refuses it", d.Repo(), c)
			}
		}
	}
}

// The working tier used to be the escape hatch: private, and therefore allowed
// to hold anything on the grounds that processing material is not publishing it.
// It is public now, so it is held to the same rule as a release and the text it
// may not redistribute never leaves the box that read it.
func TestTheWorkingTierIsHeldToTheSameRuleAsARelease(t *testing.T) {
	var working int
	for _, d := range Datasets() {
		if d.Tier != Working {
			continue
		}
		working++
		if !d.Text {
			continue
		}
		// Every class that does not publish, read off the class rather than
		// listed here, so that the rule is the rule and not a copy of it that
		// was accurate when it was written.
		for _, c := range doc.LicenseClasses() {
			if c.Publishable() {
				continue
			}
			if d.Admits(c) {
				t.Errorf("%s is a working repo carrying text and it admits %s", d.Repo(), c)
			}
		}
	}
	if working == 0 {
		t.Error("there is no working tier, so a box has nowhere to push a part it wants to delete")
	}
}

func TestAPublicTextRepoRefusesWhatItDoesNotName(t *testing.T) {
	d, ok := Lookup("vietnamese-legal-text")
	if !ok {
		t.Fatal("the legal corpus has no repo")
	}
	if !d.Admits(doc.LicenseOpen) {
		t.Error("the legal corpus refuses open material, which is all it holds")
	}
	if d.Admits(doc.LicenseRestricted) {
		t.Error("the legal corpus admits restricted material, which would put unpublishable text in a public repo")
	}
	if d.Admits(doc.LicenseUnknown) {
		t.Error("the legal corpus admits undetermined material")
	}
}

// The restricted pattern is a whole artifact rather than a consolation prize, so
// there has to be somewhere for it to go and it has to be public.
func TestRestrictedDocumentsHaveAPublicHomeWithoutTheirText(t *testing.T) {
	var found bool
	for _, d := range Datasets() {
		if !d.Admits(doc.LicenseRestricted) {
			continue
		}
		if d.Text {
			t.Errorf("%s publishes restricted text", d.Repo())
			continue
		}
		found = true
	}
	if !found {
		t.Error("restricted documents have no public repo, so most of the crawl publishes as nothing at all")
	}
}

// Wikipedia keeps its own repo because the Q7 position is to keep it apart, and a
// repo is a stronger separation than a shard.
func TestWikipediaHasARepoOfItsOwn(t *testing.T) {
	d, ok := Lookup("vietnamese-wikipedia-text")
	if !ok {
		t.Fatal("Wikipedia has no repo of its own, so its share alike term is loose in the corpus")
	}
	web, ok := Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the web corpus has no repo")
	}
	if d.Repo() == web.Repo() {
		t.Error("Wikipedia and the web corpus are the same repo")
	}
	if !strings.Contains(web.Holds, "Wikipedia") {
		t.Error("the web corpus does not say that Wikipedia is kept out of it")
	}
}

func TestEveryDatasetIsUsable(t *testing.T) {
	seen := make(map[string]bool)
	for _, d := range Datasets() {
		if d.Name == "" {
			t.Error("a dataset has no name")
			continue
		}
		if seen[d.Name] {
			t.Errorf("%s appears twice", d.Name)
		}
		seen[d.Name] = true

		if d.Holds == "" {
			t.Errorf("%s does not say what is in it", d.Name)
		}
		if len(d.Classes) == 0 {
			t.Errorf("%s admits nothing", d.Name)
		}
		for _, c := range d.Classes {
			if !c.Valid() || c == doc.LicenseUnknown {
				t.Errorf("%s admits %s", d.Name, c)
			}
		}
		if got := d.Repo(); got != Org+"/"+d.Name {
			t.Errorf("Repo = %q", got)
		}
		if !strings.HasSuffix(d.URL(), d.Repo()) {
			t.Errorf("URL %q does not end in the repo id", d.URL())
		}
	}
}

// A repo named for the stage that wrote it tells a reader which of our programs
// ran, which is the one thing they do not care about.
func TestTheNamesDescribeTheDataRatherThanTheCode(t *testing.T) {
	code := []string{"harvest", "normalize", "sift", "mill", "store", "gao-", "stage", "pipeline", "v1"}
	for _, d := range Datasets() {
		for _, bad := range code {
			if strings.Contains(d.Name, bad) {
				t.Errorf("%s is named after the code rather than the data", d.Name)
			}
		}
		// The language has to be in the name, because it is the first thing
		// anybody filters on and the second thing they read after the org. Spelled
		// out is the usual form and is what every published repo uses. A coinage
		// gets to carry it as the prefix instead, which is the whole reason vitco
		// is spelled the way it is.
		if !strings.HasPrefix(d.Name, "vietnamese-") && !strings.HasPrefix(d.Name, "vi") {
			t.Errorf("%s does not say what language it is, which is the first thing anybody filters on", d.Name)
		}
	}
}

func TestLookupTakesABareNameOrAFullRepoID(t *testing.T) {
	want, ok := Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the web corpus has no repo")
	}
	got, ok := Lookup(Org + "/vietnamese-web-text")
	if !ok {
		t.Fatal("a full repo id does not resolve")
	}
	if got.Name != want.Name {
		t.Errorf("the two forms resolve differently: %q and %q", got.Name, want.Name)
	}
	if _, ok := Lookup("vietnamese-nonsense"); ok {
		t.Error("a repo that does not exist resolves")
	}
}

// The path is a function of the snapshot and the shard, which is what makes an
// upload idempotent: pushing the same shard twice overwrites rather than
// duplicates, and a retry after a network failure is safe.
func TestADataPathRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		snapshot string
		i, n     int
	}{
		{"gao-v1.0", 0, 774},
		{"gao-v1.0", 773, 774},
		{"gao-v0.1-rc2", 1, 2},
		{"gao-v1.0", 99999, 100000},
	} {
		path := DataPath(tc.snapshot, tc.i, tc.n)
		snapshot, i, n, ok := ParseDataPath(path)
		if !ok {
			t.Errorf("ParseDataPath(%q) failed", path)
			continue
		}
		if snapshot != tc.snapshot || i != tc.i || n != tc.n {
			t.Errorf("ParseDataPath(%q) = %q %d %d", path, snapshot, i, n)
		}
		if again := DataPath(snapshot, i, n); again != path {
			t.Errorf("the path is not stable: %q became %q", path, again)
		}
	}
}

func TestADataPathRejectsWhatIsNotOne(t *testing.T) {
	for _, path := range []string{
		"",
		"data/gao-v1.0/part-00001-of-00774.jsonl.zst",
		"data/gao-v1.0/part-00001.parquet",
		"data/part-00001-of-00774.parquet",
		"gao-v1.0/part-00001-of-00774.parquet",
		"data//part-00001-of-00774.parquet",
		"data/gao-v1.0/part-001-of-774.parquet",
		// The index has to be inside the count. This one is a file somebody
		// wrote with the wrong shard count and it must not read as valid.
		"data/gao-v1.0/part-00800-of-00774.parquet",
		"data/gao-v1.0/part-00000-of-00000.parquet",
		"../data/gao-v1.0/part-00001-of-00774.parquet",
		"data/gao-v1.0/nested/part-00001-of-00774.parquet",
	} {
		if _, _, _, ok := ParseDataPath(path); ok {
			t.Errorf("ParseDataPath(%q) accepted it", path)
		}
	}
}

// Everything in one snapshot sits under one prefix, which is what lets a reader
// name a release and lets the engine skip the others by path rather than by
// opening them.
func TestASnapshotIsOnePrefix(t *testing.T) {
	d, _ := Lookup("vietnamese-web-text")
	q := d.Query("gao-v1.0")
	if !strings.Contains(q, "hf://datasets/"+d.Repo()) {
		t.Errorf("the query does not read from the repo: %s", q)
	}
	if !strings.Contains(q, "/gao-v1.0/") {
		t.Errorf("the query does not name the snapshot: %s", q)
	}
	prefix := strings.TrimSuffix(q[strings.Index(q, "hf://"):], "/*.parquet')")
	for i := range 3 {
		path := DataPath("gao-v1.0", i, 774)
		full := "hf://datasets/" + d.Repo() + "/" + path
		if !strings.HasPrefix(full, prefix) {
			t.Errorf("%s is not under the prefix the query reads, %s", full, prefix)
		}
	}
	if strings.HasPrefix("hf://datasets/"+d.Repo()+"/"+DataPath("gao-v0.9", 0, 774), prefix) {
		t.Error("another snapshot is under the same prefix, so a query for one release reads two")
	}
}

// The store of record has to be somewhere a reader can actually get to, and the
// URI in the environment has to point at the same organization the repos are in.
func TestTheStoreURIAndTheReposAgree(t *testing.T) {
	if !strings.HasSuffix(HubStore, Org) {
		t.Errorf("the store is %q and the repos are under %q", HubStore, Org)
	}
	if !strings.HasPrefix(HubStore, "hf://") {
		t.Errorf("the store URI has no scheme on it: %q", HubStore)
	}
	d, _ := Lookup("vietnamese-web-text")
	if !strings.Contains(d.Query("gao-v1.0"), Org+"/") {
		t.Error("a query does not read from the organization the store names")
	}
}

func TestTierNamesItself(t *testing.T) {
	if got := fmt.Sprint(Published); got != "published" {
		t.Errorf("Published prints as %q", got)
	}
	if got := fmt.Sprint(Working); got != "working" {
		t.Errorf("Working prints as %q", got)
	}
}

func TestDatasetsHandsOutACopy(t *testing.T) {
	got := Datasets()
	got[0].Name = "vietnamese-nonsense"
	if Datasets()[0].Name == "vietnamese-nonsense" {
		t.Error("editing the returned slice edited the table")
	}
}

// A working repo's path is a function of the input file and the part number,
// which is what makes an ingest idempotent without knowing how many parts a
// source will produce before it has produced them.
func TestAStagePathRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		snapshot   string
		file, part int
	}{
		{"glotcc-9ad140b6be3a", 0, 0},
		{"glotcc-9ad140b6be3a", 26, 3},
		{"hplt3-5b2785d5b11c", 11, 412},
		{"fineweb2-af9c13333eb9", 99999, 99999},
	} {
		path := StagePath(tc.snapshot, tc.file, tc.part)
		snapshot, file, part, ok := ParseStagePath(path)
		if !ok {
			t.Errorf("ParseStagePath(%q) failed", path)
			continue
		}
		if snapshot != tc.snapshot || file != tc.file || part != tc.part {
			t.Errorf("ParseStagePath(%q) = %q %d %d", path, snapshot, file, part)
		}
		if again := StagePath(snapshot, file, part); again != path {
			t.Errorf("the path is not stable: %q became %q", path, again)
		}
	}
}

func TestAStagePathRejectsWhatIsNotOne(t *testing.T) {
	for _, path := range []string{
		"",
		"data/glotcc/glotcc-9ad140b6be3a-00003-00000.jsonl.zst",
		"data/glotcc/glotcc-9ad140b6be3a-00000.parquet",
		"data/glotcc-9ad140b6be3a-00003-00000.parquet",
		"data//glotcc-9ad140b6be3a-00003-00000.parquet",
		"data/glotcc/glotcc-9ad140b6be3a-3-0.parquet",
		"../data/glotcc/glotcc-9ad140b6be3a-00003-00000.parquet",
		"data/glotcc/nested/glotcc-9ad140b6be3a-00003-00000.parquet",
		// A published path is not a staging path. They are different layouts
		// for different tiers and neither should read as the other.
		"data/gao-v1.0/part-00001-of-00774.parquet",
	} {
		if _, _, _, ok := ParseStagePath(path); ok {
			t.Errorf("ParseStagePath(%q) accepted it", path)
		}
	}
}

// The two layouts do not overlap, so a reader that finds a file under a
// snapshot knows which tier wrote it from the path alone.
func TestTheTwoLayoutsDoNotReadAsEachOther(t *testing.T) {
	published := DataPath("gao-v1.0", 1, 774)
	staged := StagePath("glotcc-9ad140b6be3a", 1, 0)

	if _, _, _, ok := ParseStagePath(published); ok {
		t.Errorf("a published path parses as a staging one: %s", published)
	}
	if _, _, _, ok := ParseDataPath(staged); ok {
		t.Errorf("a staging path parses as a published one: %s", staged)
	}
}

// Everything an ingest writes for one source sits under one prefix, which is
// what lets a resumed run list what is already there without listing the repo,
// and what lets the card offer the source as a config somebody can name.
func TestOneSourceIsOnePrefix(t *testing.T) {
	const snapshot = "glotcc-9ad140b6be3a"
	prefix := "data/glotcc/"
	for _, path := range []string{
		StagePath(snapshot, 0, 0),
		StagePath(snapshot, 26, 7),
		StagePath("glotcc-0000000000aa", 0, 0),
	} {
		if !strings.HasPrefix(path, prefix) {
			t.Errorf("%s is not under %s", path, prefix)
		}
	}
}

// Re-pinning a source has to leave the config name alone and still keep the two
// revisions apart, which is the whole reason the revision is in the file name
// rather than in the directory.
func TestTwoRevisionsShareADirectoryAndNotAName(t *testing.T) {
	old, new := StagePath("glotcc-9ad140b6be3a", 3, 0), StagePath("glotcc-0000000000aa", 3, 0)
	if path.Dir(old) != path.Dir(new) {
		t.Errorf("re-pinning moved the source from %s to %s, so every config naming it breaks", path.Dir(old), path.Dir(new))
	}
	if old == new {
		t.Errorf("two revisions of glotcc both write %s, so one overwrites the other", old)
	}
	for want, p := range map[string]string{"glotcc-9ad140b6be3a": old, "glotcc-0000000000aa": new} {
		got, file, part, ok := ParseStagePath(p)
		if !ok || got != want || file != 3 || part != 0 {
			t.Errorf("%s parsed as (%q, %d, %d, %v)", p, got, file, part, ok)
		}
	}
}

// A part whose directory disagrees with its name is not a part. Reading it as
// one would put a glotcc document into a query that asked for hplt3.
func TestAPartFiledUnderTheWrongSourceIsNotAPart(t *testing.T) {
	if _, _, _, ok := ParseStagePath("data/hplt3/glotcc-9ad140b6be3a-00003-00000.parquet"); ok {
		t.Error("a part filed under the wrong source parsed as a good one")
	}
}
