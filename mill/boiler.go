package mill

// The other half of the repetition problem, and the half document identity
// cannot see.
//
// A ministry site puts the same legal notice at the foot of every page. A news
// site puts the same six words above every comment box, the same share prompt
// under every article, and the same column of section names down the left of all
// of them. None of those pages is a copy of another page, so deduplication by
// document leaves every one of them in the corpus, and the notice arrives once
// per page. A host with forty thousand pages contributes its footer forty
// thousand times, which is more copies of that sentence than the corpus holds of
// any sentence anybody wrote on purpose.
//
// The unit is the line rather than the blank line separated block. After phoi a
// document is lines with the layout settled, and that is what the extractors
// emit: a nav column is one line per item, a footer is a line, a share prompt is
// a line. Blocks would glue the whole column into one lump that matches the
// column on no other page, which is the shape of furniture that gets missed
// rather than removed.
//
// Host aware because the same sentence means different things in different
// places. "Đọc thêm" repeated across one site is that site's furniture. The same
// two syllables repeated across the corpus is Vietnamese, and a pass that
// counted globally would remove the language a phrase at a time and report a
// retention figure that looked reasonable.

import (
	"sort"
	"strings"
)

// Furniture is what makes a repeated line boilerplate rather than a sentence
// somebody wrote twice.
//
// These are defaults and not findings, in the way every threshold in this
// pipeline is a default until an ablation moves it. The three of them together
// are one claim: that a line is furniture when it appears on a meaningful share
// of the pages of a host that has enough pages for a share to mean anything.
type Furniture struct {
	// MinDocuments is how many documents a host has to have contributed before
	// anything it repeats is treated as furniture. A host with three pages that
	// share a sentence is not evidence of anything, and a corpus that trimmed on
	// that evidence would be trimming noise.
	MinDocuments int

	// MinCopies is how many of a host's documents a line has to appear in.
	MinCopies int

	// MinShare is the share of the host's documents it has to appear in, which
	// is the same rule stated the way it survives a host being large. Both
	// apply, so a host with a million pages needs a real share and a host with
	// twenty needs more than two.
	MinShare float64
}

// DefaultFurniture is the rule the pipeline runs at.
func DefaultFurniture() Furniture {
	return Furniture{MinDocuments: 5, MinCopies: 3, MinShare: 0.10}
}

// Boiler counts the lines of a host's documents and then says which of them were
// furniture.
//
// It is two passes and it has to be. A line cannot be known to repeat until the
// host's other documents have been seen, so Count runs over everything and Strip
// runs over it again. The second pass is where the text is available, so it is
// also where the samples for the report are taken, which keeps the first pass
// from holding a copy of every distinct line in the corpus.
//
// What the first pass does hold is one counter per distinct line per host, keyed
// by a 64 bit hash rather than by the line. A collision costs one line wrongly
// removed from one host and the arithmetic says to expect well under one across
// a corpus of this size, which is the trade the whole package makes elsewhere
// for the same reason.
//
// A Boiler is not safe for concurrent use.
type Boiler struct {
	rule  Furniture
	hosts map[string]*hostLines
}

type hostLines struct {
	documents int
	counts    map[uint64]int32
	samples   map[uint64]string
	removed   int
}

// SampleLines is how many distinct removed lines a host keeps an example of, so
// that a report can show what the pass took out. It is a few because the report
// is read by a person and because the alternative grows with the corpus.
const SampleLines = 8

// NewBoiler returns an empty counter at the given rule.
func NewBoiler(rule Furniture) *Boiler {
	return &Boiler{rule: rule, hosts: make(map[string]*hostLines)}
}

// Count adds one document to what is known about a host. The first pass calls
// it once per document.
//
// A line that appears twice in one document counts once. Repetition inside a
// document is a different problem with its own measure in sang, and counting it
// here would let one page argue that its own refrain is the site's furniture.
func (b *Boiler) Count(host, text string) {
	h := b.host(host)
	h.documents++
	seen := make(map[uint64]bool)
	for line := range lines(text) {
		k, ok := lineKey(line)
		if !ok || seen[k] {
			continue
		}
		seen[k] = true
		h.counts[k]++
	}
}

// Strip removes the furniture from one document and says what it took.
//
// It runs after Count has seen every document, and after deduplication by
// document. That order matters: a host whose pages are copies of each other
// would have every line of them repeating, and this pass would empty all of
// them. By the time it runs those pages are one page.
func (b *Boiler) Strip(host, text string) Stripped {
	h, ok := b.hosts[host]
	// A host the first pass never saw has no furniture, which is the honest
	// answer rather than an error. It happens when a part is stripped against
	// counts taken over a different part, and the document comes back whole.
	if !ok || !b.enough(h) {
		return Stripped{Text: text, Lines: countLines(text)}
	}
	var (
		out  strings.Builder
		s    Stripped
		need = int32(b.min(h))
	)
	out.Grow(len(text))
	for line := range lines(text) {
		s.Lines++
		k, keyed := lineKey(line)
		if keyed && h.counts[k] >= need {
			s.Removed++
			h.removed++
			if len(h.samples) < SampleLines {
				if _, have := h.samples[k]; !have {
					h.samples[k] = line
				}
			}
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	s.Text = out.String()
	s.Emptied = s.Removed > 0 && strings.TrimSpace(s.Text) == ""
	return s
}

// Stripped is one document after the furniture came out.
//
// Emptied is on it because a document that is nothing but furniture is a real
// thing on the web and losing it silently is not the same as recording that it
// went. The caller decides what to do with it, which is what the reject store is
// for, and this package does not decide.
type Stripped struct {
	Text    string
	Lines   int
	Removed int
	Emptied bool
}

// Removes reports whether a line would be stripped from this host's documents.
func (b *Boiler) Removes(host, line string) bool {
	h, ok := b.hosts[host]
	if !ok || !b.enough(h) {
		return false
	}
	k, keyed := lineKey(line)
	return keyed && h.counts[k] >= int32(b.min(h))
}

// Hosts is how many hosts have been counted.
func (b *Boiler) Hosts() int { return len(b.hosts) }

// HostReport is what the pass knows about one host.
type HostReport struct {
	Host      string   `json:"host"`
	Documents int      `json:"documents"`
	Lines     int      `json:"distinct_lines"`
	Furniture int      `json:"furniture_lines"`
	Removed   int      `json:"removed"`
	Samples   []string `json:"samples,omitempty"`
}

// Reports is one line per host, worst first, so that a reader sees the sites the
// pass is doing the most to before the ones it barely touched.
func (b *Boiler) Reports() []HostReport {
	out := make([]HostReport, 0, len(b.hosts))
	for name, h := range b.hosts {
		r := HostReport{
			Host:      name,
			Documents: h.documents,
			Lines:     len(h.counts),
			Removed:   h.removed,
		}
		if b.enough(h) {
			need := int32(b.min(h))
			for _, n := range h.counts {
				if n >= need {
					r.Furniture++
				}
			}
		}
		for _, s := range h.samples {
			r.Samples = append(r.Samples, s)
		}
		sort.Strings(r.Samples)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Removed != out[j].Removed {
			return out[i].Removed > out[j].Removed
		}
		return out[i].Host < out[j].Host
	})
	return out
}

func (b *Boiler) host(name string) *hostLines {
	h, ok := b.hosts[name]
	if !ok {
		h = &hostLines{counts: make(map[uint64]int32), samples: make(map[uint64]string)}
		b.hosts[name] = h
	}
	return h
}

// enough says the host has contributed enough documents for a share of them to
// mean something.
func (b *Boiler) enough(h *hostLines) bool { return h.documents >= b.rule.MinDocuments }

// min is how many documents a line has to appear in on this host, which is
// whichever of the two rules is stricter here.
func (b *Boiler) min(h *hostLines) int {
	byShare := int(float64(h.documents)*b.rule.MinShare + 0.999999)
	if byShare > b.rule.MinCopies {
		return byShare
	}
	return b.rule.MinCopies
}

// countLines is how many lines a document holds, for the paths that report on a
// document without walking it.
func countLines(text string) int {
	n := 0
	for range lines(text) {
		n++
	}
	return n
}

// lines yields the lines of a document with the trailing newline dropped.
func lines(text string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for len(text) > 0 {
			line := text
			if i := strings.IndexByte(text, '\n'); i >= 0 {
				line, text = text[:i], text[i+1:]
			} else {
				text = ""
			}
			if !yield(line) {
				return
			}
		}
	}
}

// lineKey is a line as this pass compares it, hashed.
//
// It is the deduplication key, so a footer that one page renders with curly
// quotes and another with straight ones is one footer, and it is folded the same
// way the shingles are so that a site writing lý on one template and lí on
// another is not two sites. A line with nothing in it that survives keying is
// not a line anybody wrote, so it is left alone rather than counted: blank lines
// are layout and this pass is not about layout.
func lineKey(line string) (uint64, bool) {
	key := Key(line)
	if key == "" {
		return 0, false
	}
	return hashRunes([]rune(key)), true
}
