package kho

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/luat"
)

// Where processed data lands, and why that is what lets four machines process a
// corpus none of them could hold.
//
// The fleet has 500 GB of free disk against a corpus of 396 GB compressed, and
// every stage reads a shard and writes a shard, so nothing works if a stage has
// to keep what it has finished. Offload is the whole trick: a worker writes one
// shard of Parquet, pushes it, deletes it, and takes the next one. Peak disk per
// worker is two shards no matter how large the corpus gets, which is why a box
// with 98 GB free can process a terabyte.
//
// The target is dataset repos on the Hugging Face Hub, holding Parquet, under
// the open-index organization, which is the path ccrawl-cli already publishes
// down and the reason this is a short file rather than a project.
//
// Parquet rather than the JSONL and zstd the segments use. The segment format is
// for streaming a shard through a stage, one document at a time, and it is good
// at that. It is bad at the other thing anybody does with a corpus, which is ask
// a question of a column: how many documents from this host, what does the score
// distribution look like, how much of it is restricted. Parquet answers those by
// reading one column instead of every byte, and it answers them from the Hub
// without downloading the shard first.
//
// Descriptive names rather than stage names. A repo called gao-xay tells a
// reader which of our programs wrote it, which is the one thing they do not care
// about. A repo called vietnamese-legal-text tells them whether to click. The
// stage that produced it is in the manifest, where it belongs.
//
// Every repo here is public. There is no private tier and nothing is pushed
// that may not be published, which is a stronger rule than the one this started
// with and a shorter one to hold in your head. What it costs is the escape
// hatch: a stage can no longer park text it may not redistribute in a working
// repo on the way to somewhere else. What it buys is that the check runs on
// every write rather than on the writes somebody remembered to mark. A repo
// that carries text may only carry text that [luat] says ships, and the test
// below enforces it rather than the person creating the repo remembering it.
//
// The material that has nowhere to go under that rule stays on the box that
// made it and is deleted when the stage that needed it finishes. Disk on this
// fleet is cache, not storage.

// Org is the Hugging Face organization every gao dataset lives under. It is
// shared with the crawler and the Common Crawl surface, because a reader who
// finds one should find the rest.
const Org = "open-index"

// HubStore is what GAO_STORE is set to for a normal run. It is here rather than
// in may because the layout under it is this package's business, and it is a
// constant rather than a default because may still refuses to start without the
// variable set.
const HubStore = "hf://" + Org

// Tier is whether a dataset is a release or the material one is built from.
// Both are public. The difference is what a reader may rely on.
type Tier uint8

const (
	// Working is what a stage wrote on its way to a release: mutable, rewritten
	// when a source is pinned again, and not referenced by a signed manifest. It
	// exists so that a box can let go of a shard the moment it is up.
	Working Tier = iota

	// Published is part of a release. Immutable and referenced by a signed
	// manifest.
	Published
)

// String implements [fmt.Stringer].
func (t Tier) String() string {
	if t == Published {
		return "published"
	}
	return "working"
}

// Dataset is one repo on the Hub.
type Dataset struct {
	// Name is the repo name under [Org], describing what is in it rather than
	// which stage wrote it.
	Name string

	// Tier is whether it outlives the run.
	Tier Tier

	// Pretty is the title on the card, for a repo whose name does not make one.
	// Most of these are words with hyphens between them and title casing the
	// name is the right answer. A name that is a coinage is not: capitalizing
	// vitco gives Vitco, which reads as a typo rather than as a name.
	Pretty string

	// Holds is what is in it, in a sentence, and it is the first line of the
	// dataset card.
	Holds string

	// Text reports whether the repo carries document text. When it is false the
	// repo carries the URL, the provenance columns, and the scores, which is
	// the restricted publication pattern and is a whole artifact rather than a
	// consolation prize.
	Text bool

	// Classes is the license classes whose documents land here. A repo that
	// carries text may only name classes whose text ships, and there is a test
	// for that rather than a convention.
	Classes []doc.LicenseClass
}

// Repo returns the full repo id, which is what the Hub API and DuckDB both want.
func (d Dataset) Repo() string { return Org + "/" + d.Name }

// URL returns the page a person opens.
func (d Dataset) URL() string { return "https://huggingface.co/datasets/" + d.Repo() }

// Query returns the DuckDB expression that reads one snapshot of this dataset
// straight off the Hub. It is here so that the answer to "how do I look at this"
// is a line somebody can paste rather than a download.
func (d Dataset) Query(snapshot string) string {
	return fmt.Sprintf("read_parquet('hf://datasets/%s/%s/*.parquet')", d.Repo(), SnapshotDir(snapshot))
}

var datasets = []Dataset{
	{
		Name:    "vietnamese-web-text",
		Tier:    Published,
		Holds:   "the cleaned, deduplicated Vietnamese web, with Wikipedia kept out of it and anything that may not be redistributed kept out of it",
		Text:    true,
		Classes: []doc.LicenseClass{doc.LicenseOpen, doc.LicensePermissiveAttribution},
	},
	{
		Name:    "vietnamese-web-urls",
		Tier:    Published,
		Holds:   "one row per restricted document: the URL, the crawl timestamp, every provenance column, every score, and the dedup cluster, with the text withheld",
		Text:    false,
		Classes: []doc.LicenseClass{doc.LicenseRestricted},
	},
	{
		Name:    "vietnamese-legal-text",
		Tier:    Published,
		Holds:   "Vietnamese statutes, decrees, circulars, and gazettes, which are outside copyright protection by statute and therefore carry nothing at all",
		Text:    true,
		Classes: []doc.LicenseClass{doc.LicenseOpen},
	},
	{
		Name:    "vietnamese-wikipedia-text",
		Tier:    Published,
		Holds:   "Vietnamese Wikipedia, kept in a repo of its own so that its share alike term stays contained to it rather than reaching the corpus it would otherwise be blended into",
		Text:    true,
		Classes: []doc.LicenseClass{doc.LicensePermissiveAttribution},
	},
	{
		Name:    "vietnamese-document-text",
		Tier:    Published,
		Holds:   "text extracted from PDFs and scans, including the pre-Unicode encodings that extract as mojibake until they are transcoded",
		Text:    true,
		Classes: []doc.LicenseClass{doc.LicenseOpen, doc.LicensePermissiveAttribution},
	},
	{
		Name:    "vietnamese-speech-text",
		Tier:    Published,
		Holds:   "transcripts of Vietnamese speech, each carrying the recording it came from and the model that transcribed it",
		Text:    true,
		Classes: []doc.LicenseClass{doc.LicenseOpen, doc.LicensePermissiveAttribution},
	},
	{
		Name:    "vietnamese-synthetic-text",
		Tier:    Published,
		Holds:   "model generated Vietnamese, with its generator card, which is a separate artifact and never part of the corpus headline",
		Text:    true,
		Classes: []doc.LicenseClass{doc.LicenseOpen},
	},
	{
		Name:  "vietnamese-text-rejects",
		Tier:  Published,
		Holds: "one row per dropped document with the stage and reason code that dropped it, and no text, because failing a filter does not change a document's license",
		Text:  false,
		Classes: []doc.LicenseClass{
			doc.LicenseOpen, doc.LicensePermissiveAttribution,
			doc.LicenseRestricted, doc.LicenseUnredistributable,
		},
	},
	{
		Name:    "vitco",
		Tier:    Working,
		Pretty:  "ViTco",
		Holds:   "the pinned public Vietnamese corpora as gao read them, every source put to one contract and one schema, before any cleaning",
		Text:    true,
		Classes: []doc.LicenseClass{doc.LicenseOpen, doc.LicensePermissiveAttribution},
	},
}

// Datasets returns every repo, published tier first.
func Datasets() []Dataset {
	out := make([]Dataset, len(datasets))
	copy(out, datasets)
	return out
}

// StageRepo is the repo a stage writes its output to while a snapshot is being
// built. It is named here rather than passed around, because every stage writes
// to the same place and a stage that took the repo as an argument is a stage
// somebody can point at a published one by mistake.
const StageRepo = "vitco"

// Staging returns that repo. It panics if the table does not have it, which is
// a broken build rather than a runtime condition, since the table is a constant.
func Staging() Dataset {
	d, ok := Lookup(StageRepo)
	if !ok {
		panic("kho: " + StageRepo + " is not in the dataset table")
	}
	return d
}

// Lookup returns the dataset with the given name, which may be given bare or as
// a full repo id.
func Lookup(name string) (Dataset, bool) {
	name = strings.TrimPrefix(name, Org+"/")
	for _, d := range datasets {
		if d.Name == name {
			return d, true
		}
	}
	return Dataset{}, false
}

// The layout inside a repo.
//
// One Parquet file per shard per snapshot, in one directory per snapshot, which
// is what lets a query name one release and lets DuckDB skip the rest by path
// instead of by reading them. It is also what makes an upload idempotent: the
// path is a function of the snapshot and the shard, so pushing the same shard
// twice overwrites rather than duplicates.
//
// The directories are named plainly rather than Hive style, which is a small
// choice with two consequences worth the words. A Hive path spells the directory
// snapshot=gao-v1.0, and every reader who globs the repo then gets a snapshot
// column in their result set that is in no file, sitting beside a source column
// that is, so the first thing the layout teaches them is a distinction they did
// not ask for. The second is that the Hub's viewer takes its splits from the
// configs the card declares, and a config already names the directory, so the
// partition is doing work that is being done anyway.
const (
	// DataDir is the top level directory every dataset puts its files under,
	// which is what the Hub's own dataset viewer expects.
	DataDir = "data"

	// ParquetExt is the file extension. It is here as a constant for the same
	// reason SegmentExt is: a string literal in three places is a rename waiting
	// to go wrong in two of them.
	ParquetExt = ".parquet"
)

var dataPathPattern = regexp.MustCompile(
	`^` + DataDir + `/([A-Za-z0-9][A-Za-z0-9._-]*)/part-(\d{5,})-of-(\d{5,})\` + ParquetExt + `$`)

// SnapshotDir is the directory one snapshot's parts live under.
//
// The layout partitions by snapshot, so this doubles as the prefix that lists
// one snapshot and nothing else.
func SnapshotDir(snapshot string) string {
	return DataDir + "/" + snapshot
}

// DataPath returns the path inside a dataset repo for one shard of one snapshot.
//
//	data/gao-v1.0/part-00001-of-00774.parquet
func DataPath(snapshot string, i, n int) string {
	return fmt.Sprintf("%s/part-%05d-of-%05d%s", SnapshotDir(snapshot), i, n, ParquetExt)
}

// ParseDataPath is the inverse of [DataPath]. It reports false for a path that
// does not name a shard, including one where the index is not inside the count,
// because a part-00800-of-00774 is a file somebody generated with the wrong
// shard count and it should not read as valid.
func ParseDataPath(path string) (snapshot string, i, n int, ok bool) {
	m := dataPathPattern.FindStringSubmatch(path)
	if m == nil {
		return "", 0, 0, false
	}
	i, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, 0, false
	}
	n, err = strconv.Atoi(m[3])
	if err != nil || n == 0 || i >= n {
		return "", 0, 0, false
	}
	return m[1], i, n, true
}

// The layout inside a working repo, which is not the layout inside a published
// one and should not be.
//
// A published snapshot knows how many shards it has, so its files are named
// part-i-of-n and a directory listing says whether it is complete. An ingest
// does not. It is reading a 26.6 GB source file, writing parts as it goes, and
// rolling over to the next one whenever the current one has taken enough text
// to keep peak disk bounded, so the part count for a source is not known until
// the source finishes. Naming a file for a total nobody has yet would mean
// either guessing it or renaming every part at the end, and both of those are
// worse than a layout that does not claim to know.
//
// So the directory is the source and the file name is everything else: the
// snapshot, which is the source and the revision it was pinned at, then the
// input file the part came out of, then the part. The same input file decoded
// twice with the same part size produces the same name, so a retry after a
// dropped connection overwrites rather than duplicates, and a resumed ingest
// skips a file whose parts are already there.
//
// The directory is the source and not the snapshot because a directory in a
// working repo is a config in the card, and a config is a name somebody writes
// in a load_dataset call. Re-pinning a source has to be able to happen without
// breaking that name, and it does: the revision moves inside the file name, the
// two revisions sit side by side in the one directory, and the card says which
// of them is current. A reader who wants one revision globs for it.
var stagePathPattern = regexp.MustCompile(
	`^` + DataDir + `/([a-z0-9][a-z0-9-]*)/(.+)-(\d{5,})-(\d{5,})\` + ParquetExt + `$`)

// SourceDir is the directory one source's parts live under, whatever revision
// they were ingested at. It is the prefix that lists a source and nothing else,
// and it is the path a config in the card globs.
func SourceDir(snapshot string) string {
	return DataDir + "/" + Source(snapshot)
}

// Source is the source name a working snapshot was taken from, which is the
// snapshot with its revision cut off.
func Source(snapshot string) string {
	if source, _, ok := strings.Cut(snapshot, "-"); ok {
		return source
	}
	return snapshot
}

// StagePath returns the path inside a working repo for one part of one input
// file.
//
//	data/glotcc/glotcc-9ad140b6be3a-00003-00000.parquet
func StagePath(snapshot string, file, part int) string {
	return fmt.Sprintf("%s/%s-%05d-%05d%s", SourceDir(snapshot), snapshot, file, part, ParquetExt)
}

// ParseStagePath is the inverse of [StagePath]. It reports false for a path
// whose directory is not the source its file name claims, since a part that
// says glotcc under hplt3 is a part somebody wrote with the wrong snapshot and
// a reader globbing one source would pick it up as the other.
func ParseStagePath(path string) (snapshot string, file, part int, ok bool) {
	m := stagePathPattern.FindStringSubmatch(path)
	if m == nil {
		return "", 0, 0, false
	}
	if Source(m[2]) != m[1] {
		return "", 0, 0, false
	}
	file, err := strconv.Atoi(m[3])
	if err != nil {
		return "", 0, 0, false
	}
	part, err = strconv.Atoi(m[4])
	if err != nil {
		return "", 0, 0, false
	}
	return m[2], file, part, true
}

// carries reports whether a dataset is allowed to hold documents of a class.
func (d Dataset) carries(c doc.LicenseClass) bool {
	for _, have := range d.Classes {
		if have == c {
			return true
		}
	}
	return false
}

// Admits reports whether a document of the given class may be written to this
// dataset, which is the check an upload makes before it uploads rather than the
// check a reviewer makes after a release.
//
// A repo that carries text admits only classes whose text ships, so the legal
// position and the storage layout cannot drift apart. There is no working repo
// that admits anything on the grounds that processing is not publishing, since
// everything here is published the moment it is pushed.
func (d Dataset) Admits(c doc.LicenseClass) bool {
	if !d.carries(c) {
		return false
	}
	if d.Text && !luat.Publishes(c).Text {
		return false
	}
	return true
}
