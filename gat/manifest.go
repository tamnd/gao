// Package gat is acquisition: everything that puts a document in the store with
// its provenance attached, and nothing that happens to it afterwards.
//
// This file is the ingest manifest, which is the list of every file gao will
// download and the exact revision it will download it at. It is data rather than
// code because it is the answer to the only question anybody asks about a
// corpus that claims to be reproducible, which is what went into it.
//
// Pinned by revision, never by branch. A dataset re-uploaded upstream produces a
// new ingest with a new revision, never a silent mutation of the old one, and
// the difference between those two is the difference between a corpus somebody
// can check and a corpus somebody has to trust. [Pinned.URL] returns the
// immutable form of the address, so a stage that follows the manifest cannot
// accidentally fetch whatever is on main today.
//
// The revisions and byte counts here were read from the hosts on the pinned date
// rather than copied from the inventory, and reading them corrected the inventory
// three times. GlotCC's Vietnamese partition was described as small and is
// 55.9 GB. The full pull was estimated at roughly 490 GB and is 608.9 GB, of
// which 513.6 GB is fetched and 95.3 GB is pinned and dropped.
// CulturaX is gated, which the inventory did not say, and a gated repo does not
// hand its file digests to an unauthenticated caller. All three corrections are
// in the numbers below rather than in a note about them, which is the point of
// keeping the manifest as data.
//
// HPLT v3 is the awkward one and it is also the spine. It is not hosted on the
// Hub, so there is no commit to pin, and what it publishes instead is a per
// language map file listing the shards. The manifest pins the sha256 of that map,
// which fixes the shard list, and records every shard's byte count from a HEAD.
package gat

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/tamnd/gao/doc"
)

//go:embed manifest.json
var manifestJSON []byte

// Origin is how a source is fetched, which is a smaller set than it looks:
// either the file has a commit behind it or it does not.
type Origin uint8

const (
	// Hub is a Hugging Face dataset repo, addressed by commit SHA.
	Hub Origin = iota

	// Direct is an HTTP host the project does not control and that has no
	// revision of its own. HPLT v3 is the only one, and it is the largest source
	// in the corpus, so this is not an edge case to be tidied away later.
	Direct
)

// String implements [fmt.Stringer].
func (o Origin) String() string {
	if o == Direct {
		return "direct"
	}
	return "hub"
}

// MarshalText implements [encoding.TextMarshaler], so that the manifest holds a
// word and Go holds a small integer.
func (o Origin) MarshalText() ([]byte, error) { return []byte(o.String()), nil }

// UnmarshalText implements [encoding.TextUnmarshaler].
func (o *Origin) UnmarshalText(text []byte) error {
	switch string(text) {
	case "hub":
		*o = Hub
	case "direct":
		*o = Direct
	default:
		return fmt.Errorf("%q is not an origin", text)
	}
	return nil
}

// File is one file to download.
type File struct {
	// Path is the path inside the repo, which is also the path under the config
	// on a direct host.
	Path string `json:"path"`

	// Bytes is the size the host reported when the manifest was pinned. A
	// download that ends at a different size is a failed download and not a
	// smaller version of the same file.
	Bytes int64 `json:"bytes"`

	// Digest is the content hash the host publishes, prefixed with its
	// algorithm. It is empty for a host that publishes none, which is HPLT, and
	// an empty digest means gao computes and records its own on first fetch
	// rather than that the file is unverified forever.
	Digest string `json:"digest"`
}

// Pinned is one acquisition path frozen at one revision.
type Pinned struct {
	// Source is the acquisition path, which is the same enum every record
	// carries, so a document in the store points back at a row in this table.
	Source doc.Source `json:"source"`

	// Order is the ingest order. HPLT v3 is zero and ingests alone, because every
	// later source dedups against a store that already contains it and the
	// retention numbers are only reproducible against a fixed reference.
	Order int `json:"order"`

	// Origin is Hub or Direct.
	Origin Origin `json:"origin"`

	// Repo is the dataset repo id, or the base URL for a direct host.
	Repo string `json:"repo"`

	// Revision is the 40 hex commit SHA for a Hub source, or an algorithm
	// prefixed digest of the file that fixes the file list for a direct one.
	Revision string `json:"revision"`

	// RevisionURL is where the current revision can be read, which is what makes
	// drift detectable. It is never fetched during an ingest, only by [Check].
	RevisionURL string `json:"revision_url"`

	// Config is the language partition: vie_Latn, vie-Latn, or vi, depending on
	// which convention the producer picked.
	Config string `json:"config"`

	// Gated reports whether the host requires an accepted agreement before it
	// will serve the files. It is a field rather than a note because it has a
	// consequence in code: the Hub masks the digests of a gated repo until
	// access is granted, so a gated source pins byte counts and no digests, and
	// an ingest of one that has not been granted fails at the first fetch rather
	// than halfway through.
	Gated bool `json:"gated"`

	// Class is the license class every document from this source is admitted
	// with, which comes from the determination in luat and is repeated here so
	// that the ingest does not have to reach across packages mid-stream.
	Class doc.LicenseClass `json:"license_class"`

	// Note is why this source is in the manifest and anything about it that
	// would otherwise be discovered the hard way.
	Note string `json:"note"`

	// Dropped reports whether this source is pinned and not fetched.
	//
	// It stays in the manifest with its full file list, its byte counts and its
	// digests, because deleting it would leave a reader asking why a dataset
	// every Vietnamese corpus cites is missing, and the answer would be in a
	// commit message nobody reads. Design rule 3 is the vocabulary: a document
	// that cannot carry provenance is dropped rather than admitted with nulls,
	// and a source where that is true of every document is dropped the same way.
	// Re-admitting one is flipping this field back.
	Dropped bool `json:"dropped,omitempty"`

	// DroppedBecause is why, and it is required whenever Dropped is set. It
	// carries the evidence rather than the conclusion, because the next person
	// to consider re-admitting the source needs to know what was checked.
	DroppedBecause string `json:"dropped_because,omitempty"`

	// Files is what gets downloaded, in path order.
	Files []File `json:"files"`

	// Excluded is what the source ships that gao does not take. It is recorded
	// rather than omitted, because a reader comparing the manifest against the
	// repo should find a reason rather than a gap.
	Excluded []File `json:"excluded"`

	// ExcludedBecause is that reason, and it is required whenever Excluded is
	// not empty.
	ExcludedBecause string `json:"excluded_because"`
}

// Bytes is the download size of this source in bytes.
func (p Pinned) Bytes() int64 {
	var n int64
	for _, f := range p.Files {
		n += f.Bytes
	}
	return n
}

// ExcludedBytes is what this source ships that gao does not take.
func (p Pinned) ExcludedBytes() int64 {
	var n int64
	for _, f := range p.Excluded {
		n += f.Bytes
	}
	return n
}

// Snapshot names the working snapshot an ingest of this source writes under.
//
// It carries the revision and not only the source, because two revisions of the
// same dataset are two corpora and a partition that named the source alone would
// let a re-pinned source write its parts in among the ones the old revision left
// behind. Twelve hex digits of the commit is enough to tell two revisions apart
// and short enough to read in a path.
func (p Pinned) Snapshot() string {
	rev := p.Revision
	if _, hash, ok := strings.Cut(rev, ":"); ok {
		rev = hash
	}
	const n = 12
	if len(rev) > n {
		rev = rev[:n]
	}
	return string(p.Source) + "-" + rev
}

// IndexOf returns the position of a file in this source's file list, which is
// the partition an ingest writes that file's output under. It returns -1 for a
// file the source does not have.
func (p Pinned) IndexOf(f File) int {
	for i, have := range p.Files {
		if have.Path == f.Path {
			return i
		}
	}
	return -1
}

// URL returns the address to fetch one file from, at the pinned revision.
//
// For a Hub source that is the resolve endpoint with the commit SHA in it, which
// is immutable, rather than the one with a branch name in it, which is not. This
// is the method that makes pinning mean something at fetch time instead of only
// in the manifest.
func (p Pinned) URL(f File) string {
	if p.Origin == Direct {
		return p.Repo + "/" + f.Path
	}
	return "https://huggingface.co/datasets/" + p.Repo + "/resolve/" + p.Revision + "/" + f.Path
}

// Page returns the page a person opens to see what a source is.
func (p Pinned) Page() string {
	if p.Origin == Direct {
		return p.Repo
	}
	return "https://huggingface.co/datasets/" + p.Repo
}

type manifest struct {
	Version  int      `json:"version"`
	PinnedOn string   `json:"pinned_on"`
	Sources  []Pinned `json:"sources"`
}

var pinned = mustLoad(manifestJSON)

func mustLoad(b []byte) manifest {
	m, err := load(b)
	if err != nil {
		panic("gat: the embedded ingest manifest is not usable: " + err.Error())
	}
	return m
}

func load(b []byte) (manifest, error) {
	var m manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return manifest{}, err
	}
	if err := validate(m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

var (
	commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
	fileSHA   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// validate is the reason the manifest can be data. Everything a reviewer would
// otherwise have to check by eye is checked here, once, at load.
func validate(m manifest) error {
	if m.Version != 1 {
		return fmt.Errorf("manifest version %d is not one this build understands", m.Version)
	}
	if m.PinnedOn == "" {
		return fmt.Errorf("the manifest does not say when it was pinned")
	}
	if len(m.Sources) == 0 {
		return fmt.Errorf("the manifest pins nothing")
	}
	seenSource := make(map[doc.Source]bool, len(m.Sources))
	seenOrder := make(map[int]bool, len(m.Sources))
	for _, p := range m.Sources {
		if !p.Source.Valid() {
			return fmt.Errorf("%q is not an acquisition path", p.Source)
		}
		if seenSource[p.Source] {
			return fmt.Errorf("%s is pinned twice", p.Source)
		}
		seenSource[p.Source] = true
		if seenOrder[p.Order] {
			return fmt.Errorf("%s shares ingest order %d with another source", p.Source, p.Order)
		}
		seenOrder[p.Order] = true

		if err := validateRevision(p); err != nil {
			return err
		}
		if p.RevisionURL == "" {
			return fmt.Errorf("%s has no address to read its current revision from, so drift is undetectable", p.Source)
		}
		if p.Config == "" {
			return fmt.Errorf("%s does not name a language partition", p.Source)
		}
		if !p.Class.Valid() || p.Class == doc.LicenseUnknown {
			return fmt.Errorf("%s ingests as %s, which the contract rejects", p.Source, p.Class)
		}
		if p.Note == "" {
			return fmt.Errorf("%s does not say why it is in the manifest", p.Source)
		}
		if len(p.Files) == 0 {
			return fmt.Errorf("%s pins no files", p.Source)
		}
		if len(p.Excluded) > 0 && p.ExcludedBecause == "" {
			return fmt.Errorf("%s holds back %d files without saying why", p.Source, len(p.Excluded))
		}
		if len(p.Excluded) == 0 && p.ExcludedBecause != "" {
			return fmt.Errorf("%s gives a reason for holding back files and holds back none", p.Source)
		}
		if p.Dropped && p.DroppedBecause == "" {
			return fmt.Errorf("%s is dropped from the ingest without saying why", p.Source)
		}
		if !p.Dropped && p.DroppedBecause != "" {
			return fmt.Errorf("%s gives a reason for being dropped and is not dropped", p.Source)
		}
		if err := validateFiles(p); err != nil {
			return err
		}
	}
	return nil
}

func validateRevision(p Pinned) error {
	switch p.Origin {
	case Hub:
		if !commitSHA.MatchString(p.Revision) {
			return fmt.Errorf("%s is pinned to %q, which is not a commit SHA, so a re-run is not a re-run", p.Source, p.Revision)
		}
	case Direct:
		if !fileSHA.MatchString(p.Revision) {
			return fmt.Errorf("%s is pinned to %q, and a direct source pins the digest of the file that fixes its file list", p.Source, p.Revision)
		}
	}
	return nil
}

func validateFiles(p Pinned) error {
	seen := make(map[string]bool, len(p.Files)+len(p.Excluded))
	for _, set := range [][]File{p.Files, p.Excluded} {
		for _, f := range set {
			switch {
			case f.Path == "":
				return fmt.Errorf("%s pins a file with no path", p.Source)
			case strings.HasPrefix(f.Path, "/"), strings.Contains(f.Path, ".."):
				return fmt.Errorf("%s pins %q, which does not stay inside the repo", p.Source, f.Path)
			case f.Bytes <= 0:
				return fmt.Errorf("%s pins %s at %d bytes", p.Source, f.Path, f.Bytes)
			case seen[f.Path]:
				return fmt.Errorf("%s pins %s twice", p.Source, f.Path)
			case f.Digest != "" && !fileSHA.MatchString(f.Digest):
				return fmt.Errorf("%s pins %s with digest %q, which names no algorithm this build checks", p.Source, f.Path, f.Digest)
			case f.Digest == "" && p.Origin == Hub && !p.Gated:
				return fmt.Errorf("%s pins %s with no digest, and the Hub publishes one for every file in a repo that is not gated", p.Source, f.Path)
			}
			seen[f.Path] = true
		}
	}
	return nil
}

// Sources returns the sources an ingest fetches, in ingest order.
//
// A dropped source is not one of them. Use [AllSources] for the table a person
// reads, which has to show what was dropped and why, and this for the work a
// machine does.
func Sources() []Pinned {
	out := make([]Pinned, 0, len(pinned.Sources))
	for _, p := range pinned.Sources {
		if !p.Dropped {
			out = append(out, p.clone())
		}
	}
	return out
}

// AllSources returns every pinned source in ingest order, dropped ones included.
func AllSources() []Pinned {
	out := make([]Pinned, 0, len(pinned.Sources))
	for _, p := range pinned.Sources {
		out = append(out, p.clone())
	}
	return out
}

// Pin returns the pinned source for an acquisition path.
func Pin(s doc.Source) (Pinned, bool) {
	for _, p := range pinned.Sources {
		if p.Source == s {
			return p.clone(), true
		}
	}
	return Pinned{}, false
}

// clone copies the file lists so that a caller ranging over Sources and editing
// what it finds cannot edit the manifest for everybody else.
func (p Pinned) clone() Pinned {
	p.Files = append([]File(nil), p.Files...)
	p.Excluded = append([]File(nil), p.Excluded...)
	return p
}

// PinnedOn is the date the revisions and byte counts below were read from the
// hosts. It is in the release notes, because a manifest without a date is a
// manifest nobody can tell is stale.
func PinnedOn() string { return pinned.PinnedOn }

// TotalBytes is the whole download, which is the number that decides whether
// ingestion fits in server1's disk budget a shard at a time. Dropped sources
// are not downloaded and are not in it.
func TotalBytes() int64 {
	var n int64
	for _, p := range Sources() {
		n += p.Bytes()
	}
	return n
}

// Files is how many files the whole ingest fetches, which is also how many
// resume points it has.
func Files() int {
	var n int
	for _, p := range Sources() {
		n += len(p.Files)
	}
	return n
}

// DroppedBytes is what the manifest pins and does not fetch. It is printed
// beside the download rather than subtracted quietly, because a plan that got
// smaller and does not say so reads as a plan that was always that size.
func DroppedBytes() int64 {
	var n int64
	for _, p := range pinned.Sources {
		if p.Dropped {
			n += p.Bytes()
		}
	}
	return n
}
