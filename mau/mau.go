// Package mau decides which bytes of a layer nobody has opened get read, and
// decides it before anybody reads them.
//
// Mẫu is a sample. tang says what the layers nobody opened are worth and how
// wide the bound over them is, and the only thing that closes that bound is
// reading them. The item that has been open on this project since the 176B
// estimate was taken is the HPLT v3 buckets at 40 MB each, which against the
// real vie_Latn listing is six buckets and 240 MB of reading over 234.5 GB of
// corpus. That ratio is the whole appeal of it and also the entire problem.
// Forty megabytes is one part in two thousand of the largest bucket, and which
// part decides the answer.
//
// # Why the spread has to come from files and cannot come from offsets
//
// The obvious sample is a scatter of random offsets into the bucket, and it is
// not available. A shard is a compressed stream and a compressed stream cannot
// be entered in the middle: there is no way to know where a record starts
// without the decoder state that comes from the bytes before it. Every read this
// project can actually perform starts at the front of a file and stops when it
// has had enough.
//
// So the only dial that spreads a sample across a bucket is how many files it
// touches. Forty megabytes off one shard is forty megabytes of whatever the
// crawl put at the front of that shard, which for HPLT is a handful of domains
// crawled close together, and the rate measured on it is a rate for those
// domains. Forty megabytes taken two and a half at a time off sixteen shards
// chosen across the bucket is a reading of the bucket. Both cost the same,
// both fill the same line in a report, and only one of them answers the
// question. That is why the file count is a gate here rather than a detail, and
// why a plan reads slightly over its target rather than truncating its last file
// to hit the number exactly: a 300 kB prefix is mostly decoder warmup and one
// long document, which is the read this package exists to prevent.
//
// How far the spread actually goes is a property of the corpus rather than of
// this package, and on the corpus this was written for it does not go far. HPLT
// v3 vie_Latn ships as twelve shards over six buckets, one to four shards each,
// so a layer here is drawn across every shard it has and several layers have
// one. The plan prints the shard count beside the take and reports every layer
// under the gate as a fault, which is the honest thing it can do: the reading is
// still worth taking and it is still a rate for the shards it came off.
//
// # Why the plan is written down before the run rather than after
//
// A sample chosen after the numbers are in is not a sample. The earlier reading
// of this same corpus that came back 10% high was not anybody cheating, it was
// the top bucket being the one you reach for when you want a rate to settle
// quickly. Fixing which files get read, at a seed, before the first byte moves,
// is what makes the number that comes back a measurement rather than a choice.
//
// The seed is on the report for the same reason it is on gao dem verify: a
// third party with the seed and the listing draws the same files, so the reading
// is checkable by somebody who does not trust us. The digest is over the takes
// themselves, so a plan quietly regenerated against a different listing is a
// different digest rather than the same plan with different files in it.
package mau

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/tang"
)

// Want is what this project quotes for a bucket of HPLT v3, and the default a
// plan is sized to. It is not a statistical quantity. It is the amount of
// reading somebody worked out they could afford a bucket at a time, back when
// the bucket count was assumed to be ten and before the listing said vie_Latn
// has six. The point of naming it here is that the reading it buys gets spread
// properly rather than made bigger.
const Want = 40_000_000

// MinFiles is how many shards a layer's reading has to be spread across before
// it is a reading of the layer rather than of the front of a few of its files. A
// shard is one stretch of the crawl and sixteen stretches is enough that no
// single domain cluster carries the rate, which is the number a sample over a
// corpus sharded finely enough should want. HPLT v3 vie_Latn is not sharded that
// finely, so on it this gate is a thing the plan reports rather than a thing it
// reaches.
//
// It is a gate and not a divisor. The distinction did not exist until this was
// pointed at the real listing, where it turned out to be both: the take was
// Want/MinFiles for every layer, so a layer that has fewer shards than this read
// fewer than sixteen takes of a sixteenth each and came back under target
// without saying so. HPLT v3 vie_Latn is six buckets of one to four shards, so
// every layer of the one corpus this package was written for hit that, and the
// plan promised 40 MB a layer in its header and drew 2.5 MB in its table.
const MinFiles = 16

// MinTake is the least this plan will read off any one file. Under a megabyte a
// compressed prefix is decoder warmup and the first document or two, so the
// bytes are spent without buying a reading.
const MinTake = 1_000_000

// MinListed is how much of a layer the listing has to account for before a plan
// drawn off it is a plan over the layer. A listing is a file somebody exported,
// and an export that stopped early leaves a plan that looks exactly like a
// proper one and draws every shard it draws from whichever corner of the bucket
// made it into the file.
const MinListed = 0.95

// A File is one shard of a layer as the source's own listing gives it. Nothing
// here is measured: the listing is what the Hub or the mirror publishes before
// anything is fetched, which is the point, since the plan has to exist before
// the fetching does.
type File struct {
	Layer string `json:"layer"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

// A Take is one file and how much of its head gets read.
type Take struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Of    int64  `json:"of"`
}

// Whole reports whether the take is the entire file, which is what happens to
// shards smaller than the take size and is the one case where reading the front
// of a file is reading all of it.
func (t Take) Whole() bool { return t.Bytes >= t.Of }

// A Read is one layer's share of the plan.
type Read struct {
	Layer  string `json:"layer"`
	Rank   int    `json:"rank"`
	Stored int64  `json:"stored"`

	// Files is how many shards the layer has in the listing, against which
	// Takes is how many of them this plan opens.
	Files int    `json:"files"`
	Takes []Take `json:"takes"`

	Bytes int64   `json:"bytes"`
	Share float64 `json:"share"`

	// Listed is what those shards add up to, which is how much of the layer
	// the plan was drawn from rather than how much of it gets read.
	Listed      int64   `json:"listed"`
	ListedShare float64 `json:"listed_share"`
}

// A Plan is the reading somebody is about to do, worked out from the listing
// alone.
type Plan struct {
	Source string `json:"source"`
	Seed   string `json:"seed"`
	Want   int64  `json:"want"`

	// Digest is over the seed, the target, and every take in the plan, so a
	// run reports back the plan it ran rather than the plan it meant to.
	Digest doc.Hash `json:"digest"`

	Reads []Read `json:"reads"`
	Bytes int64  `json:"bytes"`
	Takes int    `json:"takes"`

	// Lit is the layers somebody already read, Opens is the layers this plan
	// opens, and Shut is the layers that stay closed after it runs because the
	// listing holds no files for them.
	Lit   int `json:"lit"`
	Opens int `json:"opens"`
	Shut  int `json:"shut"`

	ShutBytes int64   `json:"shut_bytes"`
	ShutShare float64 `json:"shut_share"`

	shut   []tang.Layer
	layers []tang.Layer
	files  []File
}

// ReadPlan works out what to read. The layers come from the same file tang
// reads, since the question of which layers are unread is the question tang
// already answers, and the listing is whatever the source publishes about its
// own shards.
func ReadPlan(source, seed string, want int64, layers []tang.Layer, files []File) Plan {
	p := Plan{Source: source, Seed: seed, Want: want, layers: layers, files: files}

	byLayer := map[string][]File{}
	for _, f := range files {
		byLayer[f.Layer] = append(byLayer[f.Layer], f)
	}

	var stored int64
	for _, l := range layers {
		stored += l.Stored
		switch {
		case l.Sampled():
			p.Lit++
		case len(byLayer[l.Name]) == 0:
			p.Shut++
			p.ShutBytes += l.Stored
			p.shut = append(p.shut, l)
		default:
			r := plan(l, byLayer[l.Name], seed, want)
			p.Reads = append(p.Reads, r)
			p.Bytes += r.Bytes
			p.Takes += len(r.Takes)
		}
	}
	p.Opens = len(p.Reads)
	if stored > 0 {
		p.ShutShare = float64(p.ShutBytes) / float64(stored)
	}
	p.Digest = digest(p)
	return p
}

// plan picks the files for one layer.
//
// The order is blake3 of the seed with the path, which is the draw gao dem
// verify uses, so the two protocols in this project that sample by file sample
// the same way. The takes are handed back in path order because the list is read
// by people next to a listing that is in path order.
//
// How much comes off each file is worked out here rather than once for the whole
// plan, because it depends on how many shards the layer has. A layer with two
// hundred shards spreads the target across sixteen of them and a layer with two
// spreads it across two, and both read the target. The version that divided by
// MinFiles regardless read a target's worth only when a layer had at least
// sixteen shards, and quietly read a fraction of it when it did not, which is
// the case every layer of HPLT v3 vie_Latn is in.
func plan(l tang.Layer, in []File, seed string, want int64) Read {
	// Rounded up rather than down, so that a target which does not divide by the
	// shard count is met rather than missed by the remainder. Bucket 7 of the real
	// listing is three shards and a 40 MB target, which rounded down is 13333333 a
	// shard and 39999999 read, and a plan that reports a target it did not reach is
	// the thing this file is otherwise about.
	over := int64(min(len(in), MinFiles))
	take := max((want+over-1)/over, MinTake)

	ranked := slices.Clone(in)
	rank := make(map[string]doc.Hash, len(in))
	for _, f := range in {
		rank[f.Path] = doc.SumString(seed + "\x00" + f.Path)
	}
	slices.SortFunc(ranked, func(a, b File) int {
		ra, rb := rank[a.Path], rank[b.Path]
		if c := strings.Compare(string(ra[:]), string(rb[:])); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})

	r := Read{Layer: l.Name, Rank: l.Rank, Stored: l.Stored, Files: len(in)}
	for _, f := range in {
		r.Listed += f.Bytes
	}
	for _, f := range ranked {
		if r.Bytes >= want {
			break
		}
		n := min(f.Bytes, take)
		r.Takes = append(r.Takes, Take{Path: f.Path, Bytes: n, Of: f.Bytes})
		r.Bytes += n
	}
	slices.SortFunc(r.Takes, func(a, b Take) int { return strings.Compare(a.Path, b.Path) })

	if l.Stored > 0 {
		r.Share = float64(r.Bytes) / float64(l.Stored)
		r.ListedShare = float64(r.Listed) / float64(l.Stored)
	}
	return r
}

// digest is the plan as a third party would rebuild it: the seed, the target,
// and every take, in the order the plan hands them out.
func digest(p Plan) doc.Hash {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s\n%d\n", p.Source, p.Seed, p.Want)
	for _, r := range p.Reads {
		fmt.Fprintf(&b, "%s\n", r.Layer)
		for _, t := range r.Takes {
			fmt.Fprintf(&b, "%s %d\n", t.Path, t.Bytes)
		}
	}
	return doc.SumString(b.String())
}

// Covers is the layers that will have been read once this plan runs, against
// every layer the source has.
func (p Plan) Covers() (int, int) { return p.Lit + p.Opens, len(p.layers) }

// Blocking is every reason this is not a plan anybody can run.
func (p Plan) Blocking() []string {
	var why []string
	if p.Source == "" {
		why = append(why, "the plan does not say what source it reads, and a reading that cannot be attributed cannot be weighted into anything")
	}
	if p.Seed == "" {
		why = append(why, "the plan names no seed, so nobody can draw the same files again and what comes back is a reading only we can take")
	}
	if p.Want <= 0 {
		why = append(why, "the plan reads nothing off each layer, which is the state the layers are already in")
	}
	if len(p.layers) == 0 {
		return append(why, "the source is published in layers and none of them are here")
	}
	if len(p.files) == 0 {
		return append(why, "the listing holds no files, so there is nothing to choose between")
	}

	known := map[string]bool{}
	ranks := map[int]string{}
	for _, l := range p.layers {
		if l.Name == "" {
			why = append(why, "a layer with no name cannot be matched against the listing")
			continue
		}
		if known[l.Name] {
			why = append(why, fmt.Sprintf("%s appears twice in the layers, so a file naming it belongs to either of two weights", l.Name))
		}
		known[l.Name] = true
		if l.Rank <= 0 {
			why = append(why, fmt.Sprintf("%s has no place in the ordering, and the whole question is which end of the corpus goes unread", l.Name))
		} else if first, ok := ranks[l.Rank]; ok {
			why = append(why, fmt.Sprintf("%s and %s both sit at rank %d, so the ordering does not order them", first, l.Name, l.Rank))
		} else {
			ranks[l.Rank] = l.Name
		}
		if l.Stored <= 0 {
			why = append(why, fmt.Sprintf("%s says it holds nothing on disk, so nothing decides how much of it is worth reading", l.Name))
		}
	}

	var noPath, noBytes, twice, unknown tally
	seen := map[string]bool{}
	for _, f := range p.files {
		switch {
		case f.Path == "":
			noPath.add(f.Layer)
		case seen[f.Path]:
			twice.add(f.Path)
		case f.Bytes <= 0:
			noBytes.add(f.Path)
		}
		seen[f.Path] = true
		if f.Layer == "" || !known[f.Layer] {
			unknown.add(fmt.Sprintf("%s, on %s", quoted(f.Layer), f.Path))
		}
	}
	why = append(why,
		noPath.say(
			"a file in %[2]s arrived with no path, so the plan cannot say what to fetch",
			"%[1]d files arrived with no path, the first of them in %[2]s"),
		twice.say(
			"%[2]s is listed twice, and a shard drawn twice is a shard weighted twice",
			"%[1]d files are listed twice, the first of them %[2]s"),
		noBytes.say(
			"%[2]s is listed at no size, and the size is what decides whether reading its head is reading the file",
			"%[1]d files are listed at no size, the first of them %[2]s"),
		unknown.say(
			"the listing puts a file in a layer the source does not have: %[2]s",
			"%[1]d files sit in layers the source does not have, the first of them %[2]s"),
	)

	if p.Lit == len(p.layers) {
		why = append(why, "every layer was read already, so there is nothing here to plan")
	}
	return said(why)
}

// Faults are the reasons a plan that runs is not the reading it looks like.
// None of them stop it being printed, because the argument is about what the
// reading will be worth rather than about whether the bytes can be fetched.
func (p Plan) Faults() []string {
	if len(p.Blocking()) > 0 {
		return nil
	}
	var out []string

	if p.Want < tang.MinRead {
		out = append(out, fmt.Sprintf(
			"the plan reads %s off each layer, under the %s a layer's rate needs before one long page stops moving it, so the readings it produces are not ones tang will scale anything by",
			size(p.Want), size(tang.MinRead)))
	}

	var thin, part []Read
	for _, r := range p.Reads {
		if len(r.Takes) < MinFiles {
			thin = append(thin, r)
		}
		if r.ListedShare < MinListed {
			part = append(part, r)
		}
	}
	switch n := len(thin); {
	case n == 1:
		out = append(out, fmt.Sprintf(
			"%s is read off %s, under the %d this plan spreads across, because the listing gives it %s, so the rate that comes back is a rate for those shards",
			thin[0].Layer, plural(len(thin[0].Takes), "shard"), MinFiles, plural(thin[0].Files, "shard")))
	case n > 1:
		out = append(out, fmt.Sprintf(
			"%d layers are read off fewer than %d shards each, starting with %s at %d, so what comes back off them is a rate for the shards that were drawn",
			n, MinFiles, thin[0].Layer, len(thin[0].Takes)))
	}
	switch n := len(part); {
	case n == 1:
		out = append(out, fmt.Sprintf(
			"the listing accounts for %s of %s and the layer holds %s, so the shards this plan draws from are %s of it and the rate comes back off whichever corner of the bucket got listed",
			size(part[0].Listed), part[0].Layer, size(part[0].Stored), share(part[0].ListedShare)))
	case n > 1:
		out = append(out, fmt.Sprintf(
			"%d layers are listed short of what they hold, starting with %s at %s of its %s, so the plan draws from whichever corner of each bucket got listed",
			n, part[0].Layer, share(part[0].ListedShare), size(part[0].Stored)))
	}

	if p.Shut > 0 {
		out = append(out, fmt.Sprintf(
			"%s holding %s of the source stay shut after this plan runs, starting with %s, because the listing has no files for them",
			plural(p.Shut, "layer"), share(p.ShutShare), p.shut[0].Name))
	}
	if p.ShutShare > tang.MaxDark {
		out = append(out, fmt.Sprintf(
			"the plan leaves %s of the source unread, over the %s tang allows before an estimate stops being an estimate of the source",
			share(p.ShutShare), share(tang.MaxDark)))
	}
	if under := p.under(); len(under) > 0 {
		out = append(out, fmt.Sprintf(
			"what stays shut sits below every layer this plan reads, %s of it in %s, so the rates it produces cover the rest of the corpus only if the rest reads like the cleaner end",
			share(p.ShutShare), plural(len(under), "layer")))
	}
	return out
}

// under is the layers left shut that rank below every layer the plan opens or
// that somebody already read. They are the ones that make the estimate lean
// rather than merely widen, for the reason tang gives.
func (p Plan) under() []tang.Layer {
	floor, ok := 0, false
	for _, l := range p.layers {
		if l.Sampled() {
			if !ok || l.Rank < floor {
				floor, ok = l.Rank, true
			}
		}
	}
	for _, r := range p.Reads {
		if !ok || r.Rank < floor {
			floor, ok = r.Rank, true
		}
	}
	if !ok {
		return nil
	}
	var out []tang.Layer
	for _, l := range p.shut {
		if l.Rank < floor {
			out = append(out, l)
		}
	}
	return out
}

// Holds reports whether running this plan closes the question it is for.
func (p Plan) Holds() bool { return len(p.Blocking()) == 0 && len(p.Faults()) == 0 }

// Verdict is the plan in one paragraph.
func (p Plan) Verdict() string {
	if why := p.Blocking(); len(why) > 0 {
		return why[0]
	}

	read, of := p.Covers()
	var b strings.Builder
	fmt.Fprintf(&b, "This plan reads %s off %s across %s of %s, at seed %s, which takes %s from %d of %d layers read to %d of %d.",
		size(p.Bytes), plural(p.Takes, "shard"), plural(p.Opens, "layer"), plural(len(p.layers), "layer"),
		p.Seed, p.Source, p.Lit, of, read, of)

	faults := p.Faults()
	switch n := len(faults); n {
	case 0:
		fmt.Fprint(&b, " Every layer will have been read, each of them off enough shards that no single stretch of the crawl carries its rate.")
	case 1:
		fmt.Fprintf(&b, " One reading says this is not the sample it looks like: %s.", faults[0])
	default:
		fmt.Fprintf(&b, " %d readings say this is not the sample it looks like: %s.", n, strings.Join(faults, "; and "))
	}
	return b.String()
}

// A tally counts one kind of bad line and remembers the first, since one bad
// listing writes the same fault onto every row it produced.
type tally struct {
	n     int
	first string
}

func (t *tally) add(what string) {
	if t.n == 0 {
		t.first = what
	}
	t.n++
}

func (t tally) say(one, many string) string {
	f := one
	if t.n > 1 {
		f = many
	}
	switch {
	case t.n == 0:
		return ""
	case !strings.Contains(f, "%"):
		return f
	}
	return fmt.Sprintf(f, t.n, t.first)
}

// said drops the empty sentences a tally hands back when its kind never fired.
func said(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func quoted(s string) string {
	if s == "" {
		return "no layer at all"
	}
	return fmt.Sprintf("%q", s)
}

func size(n int64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.1f TB", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
