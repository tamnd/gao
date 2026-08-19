package harvest

// What a site said about being used.
//
// robots.txt answers whether a page may be fetched. It does not answer what may
// be done with the page afterwards, and that second question is the one a
// training corpus turns on. A site that is happy to be indexed by a search
// engine may not be happy to be trained on, and since about 2023 there are three
// ways it can say so: the X-Robots-Tag header, the same directives repeated in a
// meta element, and TDMRep, the W3C reservation protocol that came out of the
// European text and data mining exception.
//
// None of them is enforced by anything. They are a machine readable statement of
// a wish, and honoring them is a choice gao makes rather than a rule it is held
// to. The point of reading all three is that the wish is usually stated in
// exactly one of them, and a crawler that reads only the one it likes gets to
// claim it saw nothing.
//
// What this does is read, not decide. A [Reservation] is what the site said,
// recorded per fetch and carried with the document, so that a decision taken
// later is taken against the statement rather than against a memory of it, and
// so that a site which changes its mind can be answered with what it said at the
// time.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/reject"
)

// A Reservation is what one page said about being kept and being trained on.
//
// The zero value reserves nothing, which is the answer for the great majority of
// pages, because the great majority of pages say nothing at all. Both flags are
// negative for that reason: a reservation is something a site adds, and a field
// that has to be set to true to mean "allowed" turns a dropped record into a
// silent ban on a corpus that has already been built.
type Reservation struct {
	// NoIndex is set when the page asked not to be kept.
	NoIndex bool

	// NoTrain is set when the page reserved the right to text and data
	// mining, or used one of the AI specific directives that mean the same
	// thing in the narrower case.
	NoTrain bool

	// Policy is the document a TDMRep reservation points at, when it points
	// at one. It is a URL and it is not fetched here. A site that reserves
	// its rights and then publishes terms for getting them is making an
	// offer, and following it up is a decision for a person.
	Policy string

	// Said is every directive that was read, in the order it was read, in
	// the spelling the site used. It is the evidence for the two flags above
	// and it is what goes in the record, because "NoTrain" is a conclusion
	// and `tdm-reservation: 1` is what happened.
	Said []string
}

// Reserved reports whether the page said anything at all.
func (r Reservation) Reserved() bool { return r.NoIndex || r.NoTrain }

// Consent is the state of a fetch in one word, for the column that carries it.
//
// Three states rather than a pair of booleans, because a document that may not
// be kept is not a document with a flag on it, and the two reservations are
// nested in practice: a site that will not be indexed will not be trained on.
//
// A page that reserved nothing is open rather than unasked. The difference is
// the whole point of the column: [doc.ConsentUnasked] means nobody looked, and
// only a fetch that looked can say open.
func (r Reservation) Consent() doc.Consent {
	switch {
	case r.NoIndex:
		return doc.ConsentNoIndex
	case r.NoTrain:
		return doc.ConsentNoTrain
	default:
		return doc.ConsentOpen
	}
}

// Signals is what each mechanism said, keyed by the mechanism, which is the form
// the record carries it in.
//
// Keyed by mechanism rather than kept as one list because the question asked of
// this column later is which mechanism a site used, not what order it wrote
// things in. A site that publishes a well known file and a site that adds a
// header to one page are doing different things, and a flat list of directives
// cannot tell them apart. Where a mechanism said several things they are joined
// in the order they were read.
func (r Reservation) Signals() map[string]string {
	if len(r.Said) == 0 {
		return nil
	}
	out := make(map[string]string, 3)
	for _, said := range r.Said {
		k := mechanism(said)
		if prev, ok := out[k]; ok {
			out[k] = prev + ", " + said
			continue
		}
		out[k] = said
	}
	return out
}

// mechanism names where a directive came from, by the shape of what was said.
//
// The three prefixes are the three ways of writing a reservation down, and they
// are distinguishable because each one is recorded in its own spelling: the
// well known file's entries carry the location they applied to, the two TDMRep
// headers carry their own names, and everything left is a robots directive.
func mechanism(said string) string {
	switch {
	case strings.HasPrefix(said, "tdmrep "):
		return "tdmrep"
	case strings.HasPrefix(said, "tdm-reservation:"), strings.HasPrefix(said, "tdm-policy:"):
		return "tdm"
	default:
		return "robots"
	}
}

// Reject is what the reservation means for the document, in the form the reject
// store takes, and it is where reading turns into honoring.
//
// Both reservations end the same way. A page that asked not to be kept is not
// kept, and a page that reserved its mining rights is not kept either, because
// gao is a training corpus and there is nothing else it would have been kept
// for. Keeping it and promising not to train on it would be a promise nobody
// downstream could check.
//
// The two are told apart in the record rather than in the decision. The detail
// is what the site said, in its own words, so that the number of documents lost
// to each kind of reservation is a number rather than a guess, and so that a
// site that asks why its pages are not in the corpus gets its own header back.
func (r Reservation) Reject() (reject.Reason, string, bool) {
	if !r.Reserved() {
		return "", "", false
	}
	return reject.ReasonRobots, string(r.Consent()) + ": " + strings.Join(r.Said, ", "), true
}

// Merge is the reservation of two statements about the same page.
//
// A site may say one thing in a header and another in the markup, and the two
// are not ranked anywhere, so the restrictive reading wins. That is the only
// safe way to combine them: reading two statements and honoring the permissive
// one turns a site saying no twice into a site saying yes.
func (r Reservation) Merge(o Reservation) Reservation {
	m := Reservation{
		NoIndex: r.NoIndex || o.NoIndex,
		NoTrain: r.NoTrain || o.NoTrain,
		Policy:  r.Policy,
		Said:    append(slices.Clone(r.Said), o.Said...),
	}
	if m.Policy == "" {
		m.Policy = o.Policy
	}
	return m
}

// ReadHeaders reads the reservations a response carried.
//
// agent is the name gao publishes for itself, and it decides which X-Robots-Tag
// lines apply: a line may be addressed to one crawler by name, and a line with
// no name on it is addressed to everybody. Matching is case insensitive because
// the header is written by hand and half of it is capitalised.
func ReadHeaders(h http.Header, agent string) Reservation {
	var r Reservation
	for _, line := range h.Values("X-Robots-Tag") {
		for _, d := range robotsTag(line, agent) {
			r.read(d)
		}
	}
	// TDMRep states the reservation and the policy in two headers, and the
	// policy is worth recording even when the reservation is absent, since a
	// site that publishes terms and forgets the flag has still published
	// terms.
	if v := strings.TrimSpace(h.Get("TDM-Reservation")); v != "" {
		if v == "1" {
			r.NoTrain = true
		}
		r.Said = append(r.Said, "tdm-reservation: "+v)
	}
	if v := strings.TrimSpace(h.Get("TDM-Policy")); v != "" {
		r.Policy = v
		r.Said = append(r.Said, "tdm-policy: "+v)
	}
	return r
}

// Meta reads one meta element out of the markup, by its name and its content.
//
// The names that carry a reservation are `robots`, which holds the same
// directives as the header, `tdm-reservation` and `tdm-policy`, and the AI
// specific `noai` and `noimageai`, which some publishers write as a name with an
// empty content rather than as a content value. Everything else is ignored.
//
// Extracting the elements is somebody else's job. This takes what was found,
// because the crawler has already parsed the document and a second parse here
// would be a second chance to disagree with it about what the page said.
func (r *Reservation) Meta(name, content string) {
	name = strings.ToLower(strings.TrimSpace(name))
	content = strings.TrimSpace(content)
	switch name {
	case "robots", "noai", "noimageai":
		if name != "robots" {
			r.read(name)
		}
		for _, d := range strings.Split(content, ",") {
			r.read(strings.ToLower(strings.TrimSpace(d)))
		}
	case "tdm-reservation":
		if content == "1" {
			r.NoTrain = true
		}
		if content != "" {
			r.Said = append(r.Said, "tdm-reservation: "+content)
		}
	case "tdm-policy":
		if content != "" {
			r.Policy = content
			r.Said = append(r.Said, "tdm-policy: "+content)
		}
	}
}

// read takes one directive, already lower cased and trimmed.
func (r *Reservation) read(d string) {
	switch d {
	case "":
		return
	case "noindex", "none":
		r.NoIndex = true
	case "noai", "noimageai":
		r.NoTrain = true
	default:
		// Everything else is a directive about how a search engine
		// should present the page, and none of gao's business. It is
		// still recorded, because what a site said is the record.
	}
	r.Said = append(r.Said, d)
}

// directives is every X-Robots-Tag directive with a name of its own, so that the
// colon in `unavailable_after: 25 Jun 2010` is not read as a crawler being
// addressed by name.
//
// This is the one piece of syntax in the header worth being careful about. The
// rule is that a line may begin with a crawler name and a colon, and the
// directives that take a value also carry a colon, so the two are told apart by
// knowing the directive names rather than by counting colons.
var directives = map[string]bool{
	"all": true, "none": true, "noindex": true, "nofollow": true,
	"noarchive": true, "nosnippet": true, "notranslate": true,
	"noimageindex": true, "nocache": true, "indexifembedded": true,
	"unavailable_after": true, "max-snippet": true,
	"max-image-preview": true, "max-video-preview": true,
	"noai": true, "noimageai": true,
}

// robotsTag splits one X-Robots-Tag line into the directives that apply to us.
func robotsTag(line, agent string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	// A crawler name, when there is one, is the first thing on the line, so it
	// is only a candidate when the colon comes before the first comma.
	// Otherwise the colon belongs to a directive further along, as it does in
	// "noindex, unavailable_after: 25 Jun 2010".
	if colon := strings.Index(line, ":"); colon >= 0 {
		comma := strings.Index(line, ",")
		name := strings.ToLower(strings.TrimSpace(line[:colon]))
		if (comma < 0 || colon < comma) && !directives[name] {
			// The line is addressed to one crawler by name.
			if !strings.EqualFold(name, agent) {
				return nil
			}
			line = line[colon+1:]
		}
	}

	var out []string
	for _, d := range strings.Split(line, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if name, _, ok := strings.Cut(d, ":"); ok && directives[strings.TrimSpace(name)] {
			d = strings.TrimSpace(name)
		}
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// A TDMRep is the reservations a site published at /.well-known/tdmrep.json.
//
// It is the part of the protocol that scales, because it states the reservation
// for a whole site in one file rather than on every response, and it is the part
// a crawler has to go and ask for. Sites that use TDMRep at all mostly use this.
type TDMRep struct{ rules []tdmRule }

type tdmRule struct {
	prefix   string
	wildcard bool
	reserved bool
	policy   string
}

// ReadTDMRep parses the well known file.
//
// The format is a list of objects, each naming a location and what is reserved
// under it. A location ending in `*` covers everything below it and any other
// location is one exact path, which is the reading the specification gives and
// which is worth being strict about: a site that reserved `/blog/*` did not
// reserve `/blogroll`.
func ReadTDMRep(data []byte) (*TDMRep, error) {
	var raw []struct {
		Location    string `json:"location"`
		Reservation *int   `json:"tdm-reservation"`
		Policy      string `json:"tdm-policy"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	t := &TDMRep{}
	for _, e := range raw {
		if e.Location == "" || e.Reservation == nil {
			continue
		}
		r := tdmRule{prefix: e.Location, reserved: *e.Reservation == 1, policy: e.Policy}
		if strings.HasSuffix(r.prefix, "*") {
			r.prefix, r.wildcard = strings.TrimSuffix(r.prefix, "*"), true
		}
		t.rules = append(t.rules, r)
	}
	return t, nil
}

// For is what the file says about one path on the site.
//
// The longest location that matches wins, so a site can reserve everything and
// then release one directory, which is how these files are actually written. A
// path no location matches is a path the file says nothing about.
func (t *TDMRep) For(path string) Reservation {
	if t == nil {
		return Reservation{}
	}
	best := -1
	var found tdmRule
	for _, r := range t.rules {
		switch {
		case r.wildcard && strings.HasPrefix(path, r.prefix):
		case !r.wildcard && path == r.prefix:
		default:
			continue
		}
		if len(r.prefix) > best {
			best, found = len(r.prefix), r
		}
	}
	if best < 0 {
		return Reservation{}
	}
	var res Reservation
	if found.reserved {
		res.NoTrain = true
		res.Said = append(res.Said, "tdmrep "+found.prefix+": reserved")
	} else {
		res.Said = append(res.Said, "tdmrep "+found.prefix+": not reserved")
	}
	res.Policy = found.policy
	return res
}
