// Package pack weighs a release: what it costs on disk, column by column and
// shard by shard.
//
// Gói is to wrap something up into a package, and what this measures is the
// wrapping rather than the rice. Two of the S6 predictions are about it. P06-1
// says the natural corpus publishes in under 420 GB of Parquet, and P06-4 says
// the metadata columns cost under 12% of the release bytes. Neither is a
// question about the corpus, and both decide things about it: the first decides
// whether the release fits where it is going, and the second decides whether the
// provenance columns this project insists on are affordable at half a billion
// documents or are a rule that quietly gets dropped the first time somebody
// looks at a storage bill.
//
// The measurement is taken out of the Parquet footers rather than by reading
// the files. That is not an optimization. It is the difference between a check
// that runs on the fleet and one that needs the release resident on the box
// taking it, and the fleet has one box with 5 GB free against a release of a few
// hundred gigabytes. The bytes actually read are counted and reported next to
// the bytes weighed, because a claim about how little was read is the kind of
// claim that stops being true after a library upgrade and nobody notices.
//
// What it refuses is more important than what it reports. A footprint is a sum,
// sums do not complain, and the ways this one goes wrong all look like a number.
// Two snapshots summed into one total reads as a release twice its size. A
// staging repo that withholds text summed with a published one reads as a
// release whose text got cheaper. Both are one glob away at any time, so the
// shards are checked against each other before anything is added up, and a
// release that is not one shape is refused rather than totalled.
package pack

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/tamnd/gao/store"
)

// Ceiling is P06-1: the natural corpus publishes in under 420 GB of Parquet.
const Ceiling int64 = 420 << 30

// MaxMetadata is P06-4: the columns that are not the text cost under 12% of
// the release bytes.
const MaxMetadata = 0.12

// TargetShard is what a published shard is meant to weigh, and Band is how far
// off it one may sit and still be that shard. 512 MB is small enough to fetch
// over a bad link and large enough that half a billion documents do not become
// a hundred thousand files, which is the number that makes a Hub repo unusable
// rather than large.
const (
	TargetShard int64 = 512 << 20
	Band              = 0.25
)

// Text is the column the corpus is for. Everything else is metadata, which is
// the split P06-4 is written across.
const Text = store.TextColumn

// A Column is what one column costs across the whole release.
type Column struct {
	Name         string `json:"name"`
	Compressed   int64  `json:"compressed"`
	Uncompressed int64  `json:"uncompressed"`
}

// Share is what fraction of the release's compressed bytes this column is.
func (c Column) Share(total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(c.Compressed) / float64(total)
}

// A Release is a set of shards weighed together.
type Release struct {
	Name  string
	Parts []store.PartWeight
}

// Weigh reads the footer of every path and returns them as one release.
func Weigh(name string, paths []string) (Release, error) {
	r := Release{Name: name}
	for _, p := range paths {
		w, err := store.WeighPart(p)
		if err != nil {
			return Release{}, err
		}
		r.Parts = append(r.Parts, w)
	}
	return r, nil
}

// Bytes is what the release takes on disk, as the filesystem has it. It is the
// file sizes rather than the column totals, since the footers, the page headers
// and the magic are bytes somebody stores too.
func (r Release) Bytes() int64 {
	var n int64
	for _, p := range r.Parts {
		n += p.Bytes
	}
	return n
}

// Read is what weighing the release cost.
func (r Release) Read() int64 {
	var n int64
	for _, p := range r.Parts {
		n += p.Footer
	}
	return n
}

// Rows is how many documents the release holds.
func (r Release) Rows() int64 {
	var n int64
	for _, p := range r.Parts {
		n += p.Rows
	}
	return n
}

// Columns returns every column of the release, heaviest first.
func (r Release) Columns() []Column {
	byName := map[string]*Column{}
	for _, p := range r.Parts {
		for _, c := range p.Columns {
			col, ok := byName[c.Name]
			if !ok {
				col = &Column{Name: c.Name}
				byName[c.Name] = col
			}
			col.Compressed += c.Compressed
			col.Uncompressed += c.Uncompressed
		}
	}
	out := make([]Column, 0, len(byName))
	for _, c := range byName {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Compressed != out[j].Compressed {
			return out[i].Compressed > out[j].Compressed
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Stored is the compressed total over the columns, which is the file bytes less
// the framing.
func (r Release) Stored() int64 {
	var n int64
	for _, c := range r.Columns() {
		n += c.Compressed
	}
	return n
}

// TextBytes is what the corpus itself costs, and Metadata is what everything
// else does.
func (r Release) TextBytes() int64 {
	for _, c := range r.Columns() {
		if c.Name == Text {
			return c.Compressed
		}
	}
	return 0
}

// Metadata is the compressed cost of every column that is not the text.
func (r Release) Metadata() int64 { return r.Stored() - r.TextBytes() }

// Share is the metadata's fraction of the release, which is what P06-4 is
// written against.
func (r Release) Share() float64 {
	if r.Stored() <= 0 {
		return 0
	}
	return float64(r.Metadata()) / float64(r.Stored())
}

// Ratio is what the codec bought, as the uncompressed bytes over the stored
// ones.
func (r Release) Ratio() float64 {
	var raw int64
	for _, c := range r.Columns() {
		raw += c.Uncompressed
	}
	if r.Stored() <= 0 {
		return 0
	}
	return float64(raw) / float64(r.Stored())
}

// Snapshot returns the snapshot the shards are stamped with, or the empty
// string when they do not agree, which [Release.Blocking] refuses separately.
func (r Release) Snapshot() string {
	var name string
	for _, p := range r.Parts {
		s := p.Metadata["gao.snapshot"]
		if name == "" {
			name = s
			continue
		}
		if s != name {
			return ""
		}
	}
	return name
}

// Loose returns the shards more than Band away from TargetShard, smallest
// first. A release whose shards are all the wrong size still adds up, so this
// is reported rather than refused.
func (r Release) Loose() []store.PartWeight {
	low := int64(float64(TargetShard) * (1 - Band))
	high := int64(float64(TargetShard) * (1 + Band))
	var out []store.PartWeight
	for _, p := range r.Parts {
		if p.Bytes < low || p.Bytes > high {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Bytes < out[j].Bytes })
	return out
}

// Blocking returns everything that stops these shards being one release. Each
// of them is a way a glob produces a number rather than a fault.
func (r Release) Blocking() []string {
	if len(r.Parts) == 0 {
		return []string{"no shard was weighed, so there is nothing to add up"}
	}

	var why []string
	snapshots := map[string][]string{}
	versions := map[string][]string{}
	for _, p := range r.Parts {
		snapshot := p.Metadata["gao.snapshot"]
		if snapshot == "" {
			why = append(why, fmt.Sprintf("%s carries no snapshot stamp, so nothing says which release it belongs to", p.Path))
		} else {
			snapshots[snapshot] = append(snapshots[snapshot], p.Path)
		}
		if v := p.Metadata["gao.schema_version"]; v != "" {
			versions[v] = append(versions[v], p.Path)
		}
		if p.Rows == 0 {
			why = append(why, fmt.Sprintf("%s holds no rows, and an empty shard in a release is a stage that failed quietly", p.Path))
		}
		if stored, _ := p.Weight(); stored > p.Bytes {
			why = append(why, fmt.Sprintf("%s claims %s of columns inside %s of file, so its footer does not describe it",
				p.Path, bytes(stored), bytes(p.Bytes)))
		}
	}

	if len(snapshots) > 1 {
		why = append(why, fmt.Sprintf("%s were weighed together, and two snapshots summed read as one release twice the size",
			list(names(snapshots))))
	}
	if len(versions) > 1 {
		why = append(why, fmt.Sprintf("the shards were written at schema %s, and columns that changed meaning between versions do not add up",
			list(names(versions))))
	}
	why = append(why, r.mixed()...)
	sort.Strings(why)
	return why
}

// mixed reports shards that do not carry the same columns as the rest. The case
// this is here for is a repo that withholds text weighed alongside one that
// carries it, which reads as a release whose text became cheap.
func (r Release) mixed() []string {
	if len(r.Parts) < 2 {
		return nil
	}
	first := columnNames(r.Parts[0])
	var why []string
	for _, p := range r.Parts[1:] {
		got := columnNames(p)
		if slices.Equal(got, first) {
			continue
		}
		missing := difference(first, got)
		extra := difference(got, first)
		switch {
		case len(missing) > 0 && len(extra) > 0:
			why = append(why, fmt.Sprintf("%s carries %s where %s carries %s, so these are two formats rather than one release",
				p.Path, list(extra), r.Parts[0].Path, list(missing)))
		case len(missing) > 0:
			why = append(why, fmt.Sprintf("%s does not carry %s and %s does, so a repo that withholds a column was weighed with one that ships it",
				p.Path, list(missing), r.Parts[0].Path))
		default:
			why = append(why, fmt.Sprintf("%s carries %s and %s does not, so a repo that withholds a column was weighed with one that ships it",
				p.Path, list(extra), r.Parts[0].Path))
		}
	}
	return why
}

// Holds reports whether the release cleared P06-1 and P06-4.
func (r Release) Holds() bool {
	return len(r.Blocking()) == 0 && r.Bytes() < Ceiling && r.Share() < MaxMetadata
}

// Verdict is the footprint in one paragraph.
func (r Release) Verdict() string {
	if why := r.Blocking(); len(why) > 0 {
		return fmt.Sprintf("These shards are not one release, so they are not added up. %s", why[0])
	}

	head := fmt.Sprintf("%s weighs %s over %s, read out of %s of footers.",
		r.Name, bytes(r.Bytes()), plural(len(r.Parts), "shard"), bytes(r.Read()))
	heaviest := r.Columns()[0]
	switch {
	case r.Bytes() >= Ceiling:
		return fmt.Sprintf("%s That is over the %s P06-1 claims the natural corpus publishes in, so the prediction misses and the release notes carry the real number. %s is the heaviest column at %s of the total, which is where a smaller release would have to come from.",
			head, bytes(Ceiling), heaviest.Name, percent(heaviest.Share(r.Stored())))
	case r.Share() >= MaxMetadata:
		return fmt.Sprintf("%s The columns that are not the text cost %s of it against the %s P06-4 claims, so the provenance this project insists on is more expensive than it was predicted to be, and that is the number to argue about rather than the rule.",
			head, percent(r.Share()), percent(MaxMetadata))
	}
	return fmt.Sprintf("%s It fits inside the %s P06-1 claims, the columns that are not the text cost %s of it against the %s P06-4 allows, and the codec bought %.1fx. %s is the heaviest column at %s of the total.",
		head, bytes(Ceiling), percent(r.Share()), percent(MaxMetadata), r.Ratio(), heaviest.Name, percent(heaviest.Share(r.Stored())))
}

func columnNames(p store.PartWeight) []string {
	out := make([]string, 0, len(p.Columns))
	for _, c := range p.Columns {
		out = append(out, c.Name)
	}
	slices.Sort(out)
	return out
}

func difference(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

func names[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func list(ss []string) string {
	switch len(ss) {
	case 0:
		return "nothing"
	case 1:
		return ss[0]
	case 2:
		return ss[0] + " and " + ss[1]
	}
	return strings.Join(ss[:len(ss)-1], ", ") + " and " + ss[len(ss)-1]
}

func bytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func percent(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
