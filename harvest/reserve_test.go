package harvest

import (
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/reject"
)

// The agent name in these tests is the one gao publishes for itself. It is here
// as a constant so that the tests for the lines addressed to somebody else are
// obviously addressed to somebody else.
const agent = "gaobot"

func headers(pairs ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		h.Add(pairs[i], pairs[i+1])
	}
	return h
}

func TestAPageThatSaysNothingReservesNothing(t *testing.T) {
	r := ReadHeaders(headers("Content-Type", "text/html"), agent)
	if r.Reserved() {
		t.Errorf("a response with no reservation on it came back as %+v", r)
	}
	if r.Consent() != doc.ConsentOpen {
		t.Errorf("the consent state is %q and it should be open", r.Consent())
	}
	if len(r.Said) != 0 {
		t.Errorf("nothing was said and %v was recorded", r.Said)
	}
}

func TestTheHeaderIsRead(t *testing.T) {
	for _, tt := range []struct {
		name    string
		line    string
		index   bool
		train   bool
		consent doc.Consent
	}{
		{"noindex", "noindex", true, false, doc.ConsentNoIndex},
		{"none is both", "none", true, false, doc.ConsentNoIndex},
		{"noai", "noai", false, true, doc.ConsentNoTrain},
		{"noimageai", "noimageai", false, true, doc.ConsentNoTrain},
		{"a list", "noarchive, nosnippet, noai", false, true, doc.ConsentNoTrain},
		{"capitals", "NoIndex", true, false, doc.ConsentNoIndex},
		{"one for us by name", agent + ": noai", false, true, doc.ConsentNoTrain},
		{"one for somebody else", "googlebot: noindex", false, false, doc.ConsentOpen},
		{"nothing we care about", "nofollow, noarchive", false, false, doc.ConsentOpen},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := ReadHeaders(headers("X-Robots-Tag", tt.line), agent)
			if r.NoIndex != tt.index || r.NoTrain != tt.train {
				t.Errorf("%q read as %+v", tt.line, r)
			}
			if r.Consent() != tt.consent {
				t.Errorf("%q left the consent state at %q and it should be %q", tt.line, r.Consent(), tt.consent)
			}
		})
	}
}

// A directive that takes a value carries a colon of its own, and a line that
// begins with a crawler name carries one too. Telling them apart by counting
// colons reads unavailable_after as a crawler called unavailable_after, which is
// not us, and drops the rest of the line.
func TestADirectiveWithAValueIsNotACrawlerBeingAddressedByName(t *testing.T) {
	r := ReadHeaders(headers("X-Robots-Tag", "noindex, unavailable_after: 25 Jun 2010 15:00:00 PST"), agent)
	if !r.NoIndex {
		t.Errorf("the noindex was lost: %+v", r)
	}
	if !slices.Contains(r.Said, "unavailable_after") {
		t.Errorf("the directive was not recorded: %v", r.Said)
	}
}

func TestEveryLineOfTheHeaderIsRead(t *testing.T) {
	h := headers(
		"X-Robots-Tag", "googlebot: noindex",
		"X-Robots-Tag", "nosnippet",
		"X-Robots-Tag", agent+": noai",
	)
	r := ReadHeaders(h, agent)
	if r.NoIndex {
		t.Error("a line addressed to another crawler was applied to us")
	}
	if !r.NoTrain {
		t.Error("the line addressed to us was not applied")
	}
	if want := []string{"nosnippet", "noai"}; !slices.Equal(r.Said, want) {
		t.Errorf("recorded %v and should have recorded %v", r.Said, want)
	}
}

func TestTDMRepInTheHeaders(t *testing.T) {
	r := ReadHeaders(headers(
		"TDM-Reservation", "1",
		"TDM-Policy", "https://example.vn/tdm-policy.json",
	), agent)
	if !r.NoTrain {
		t.Error("a reservation of 1 did not reserve anything")
	}
	if r.Policy != "https://example.vn/tdm-policy.json" {
		t.Errorf("the policy came back as %q", r.Policy)
	}

	open := ReadHeaders(headers("TDM-Reservation", "0"), agent)
	if open.Reserved() {
		t.Errorf("a reservation of 0 reserved something: %+v", open)
	}
	if len(open.Said) != 1 {
		t.Errorf("a site saying it reserves nothing said something, and %v was recorded", open.Said)
	}
}

func TestWhatWasSaidIsRecordedInTheSpellingTheSiteUsed(t *testing.T) {
	r := ReadHeaders(headers("X-Robots-Tag", "noarchive, NOAI", "TDM-Reservation", "1"), agent)
	want := []string{"noarchive", "noai", "tdm-reservation: 1"}
	if !slices.Equal(r.Said, want) {
		t.Errorf("recorded %v and should have recorded %v", r.Said, want)
	}
}

func TestTheMarkupIsReadTheSameWayAsTheHeader(t *testing.T) {
	var r Reservation
	r.Meta("robots", "index, noai")
	if !r.NoTrain {
		t.Errorf("the meta element was not read: %+v", r)
	}

	// Some publishers write the directive as the name of the element with
	// nothing in it.
	var bare Reservation
	bare.Meta("noai", "")
	if !bare.NoTrain {
		t.Errorf("a bare noai element was not read: %+v", bare)
	}

	var tdm Reservation
	tdm.Meta("tdm-reservation", "1")
	tdm.Meta("tdm-policy", "https://example.vn/policy")
	if !tdm.NoTrain || tdm.Policy != "https://example.vn/policy" {
		t.Errorf("the tdm elements were not read: %+v", tdm)
	}

	var other Reservation
	other.Meta("viewport", "width=device-width")
	other.Meta("description", "noai is mentioned in this description")
	if other.Reserved() || len(other.Said) != 0 {
		t.Errorf("an element that is not a reservation was read as one: %+v", other)
	}
}

// A site may say one thing in the header and another in the markup. Reading both
// and honoring the permissive one turns a site that said no twice into a site
// that said yes.
func TestTheRestrictiveReadingWins(t *testing.T) {
	header := ReadHeaders(headers("X-Robots-Tag", "noai"), agent)
	var markup Reservation
	markup.Meta("robots", "noindex")
	markup.Meta("tdm-policy", "https://example.vn/policy")

	m := header.Merge(markup)
	if !m.NoIndex || !m.NoTrain {
		t.Errorf("the two statements came together as %+v", m)
	}
	if m.Policy != "https://example.vn/policy" {
		t.Errorf("the policy was lost: %+v", m)
	}
	if want := []string{"noai", "noindex", "tdm-policy: https://example.vn/policy"}; !slices.Equal(m.Said, want) {
		t.Errorf("recorded %v and should have recorded %v", m.Said, want)
	}
	if header.NoIndex || len(header.Said) != 1 {
		t.Errorf("merging changed the statement it was called on: %+v", header)
	}
}

const wellKnown = `[
  {"location": "/*", "tdm-reservation": 1, "tdm-policy": "https://baodientu.vn/tdm.json"},
  {"location": "/thong-cao-bao-chi/*", "tdm-reservation": 0},
  {"location": "/gioi-thieu", "tdm-reservation": 0}
]`

func TestTheWellKnownFileIsReadLongestLocationFirst(t *testing.T) {
	tdm, err := ReadTDMRep([]byte(wellKnown))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path     string
		reserved bool
	}{
		{"/", true},
		{"/tin-tuc/mot-bai-viet", true},
		{"/thong-cao-bao-chi/2026/thang-tam", false},
		{"/gioi-thieu", false},
		// The exact location covers itself and nothing below it, and the
		// wildcard covers the path it names and not one that merely
		// starts with the same letters.
		{"/gioi-thieu/lien-he", true},
		{"/thong-cao-bao-chi-cu", true},
	} {
		r := tdm.For(tt.path)
		if r.NoTrain != tt.reserved {
			t.Errorf("%s came back as %+v and it should be reserved: %v", tt.path, r, tt.reserved)
		}
		if len(r.Said) != 1 {
			t.Errorf("%s recorded %v", tt.path, r.Said)
		}
	}
}

func TestAPathTheWellKnownFileSaysNothingAboutIsLeftAlone(t *testing.T) {
	tdm, err := ReadTDMRep([]byte(`[{"location": "/blog/*", "tdm-reservation": 1}]`))
	if err != nil {
		t.Fatal(err)
	}
	r := tdm.For("/blogroll")
	if r.Reserved() || len(r.Said) != 0 {
		t.Errorf("/blogroll came back as %+v, and only /blog/ was reserved", r)
	}
}

func TestAWellKnownFileThatIsNotOneIsAnError(t *testing.T) {
	if _, err := ReadTDMRep([]byte("<html>404 not found</html>")); err == nil {
		t.Error("a 404 page parsed as a reservation file")
	}
	// An entry with no reservation in it states nothing, and an entry with
	// no location has nothing to state it about. Neither is worth failing the
	// file over, because the rest of the file is still what the site said.
	tdm, err := ReadTDMRep([]byte(`[{"location": "/a/*"}, {"tdm-reservation": 1}, {"location": "/b/*", "tdm-reservation": 1}]`))
	if err != nil {
		t.Fatal(err)
	}
	if tdm.For("/a/x").Reserved() {
		t.Error("an entry with no reservation reserved something")
	}
	if !tdm.For("/b/x").Reserved() {
		t.Error("the entry after the broken ones was dropped")
	}
}

func TestNoFileAtAllIsNotAReservation(t *testing.T) {
	var missing *TDMRep
	if r := missing.For("/anything"); r.Reserved() {
		t.Errorf("a site with no well known file came back as %+v", r)
	}
}

// Reading a reservation and then not acting on it is worse than not reading it,
// because it produces a record that says the site was asked.
func TestAReservationIsHonoredAndSaysWhatInItsOwnWords(t *testing.T) {
	open := ReadHeaders(headers("X-Robots-Tag", "nosnippet"), agent)
	if _, _, rejected := open.Reject(); rejected {
		t.Error("a page that reserved nothing was rejected")
	}

	for _, tt := range []struct {
		name   string
		line   string
		detail string
	}{
		{"indexing", "noindex", "no-index: noindex"},
		{"mining", "noai", "no-train: noai"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := ReadHeaders(headers("X-Robots-Tag", tt.line), agent)
			reason, detail, rejected := r.Reject()
			if !rejected {
				t.Fatalf("%q was not honored", tt.line)
			}
			if reason != reject.ReasonRobots {
				t.Errorf("the reason is %q", reason)
			}
			if detail != tt.detail {
				t.Errorf("the detail is %q and should be %q", detail, tt.detail)
			}
		})
	}
}

// The record carries one column per mechanism, because the question asked of it
// later is which mechanism a site used. A flat list of directives cannot tell a
// site that publishes a well known file from one that adds a header to one page.
func TestWhatWasSaidIsGroupedByTheMechanismThatSaidIt(t *testing.T) {
	r := ReadHeaders(headers(
		"X-Robots-Tag", "noarchive, noai",
		"TDM-Reservation", "1",
		"TDM-Policy", "https://example.vn/policy",
	), agent)

	tdm, err := ReadTDMRep([]byte(`[{"location": "/tin-tuc/*", "tdm-reservation": 1}]`))
	if err != nil {
		t.Fatal(err)
	}
	got := r.Merge(tdm.For("/tin-tuc/mot-bai-viet")).Signals()

	want := map[string]string{
		"robots": "noarchive, noai",
		"tdm":    "tdm-reservation: 1, tdm-policy: https://example.vn/policy",
		"tdmrep": "tdmrep /tin-tuc/: reserved",
	}
	if !maps.Equal(got, want) {
		t.Errorf("recorded %v and should have recorded %v", got, want)
	}
}

func TestAPageThatSaidNothingCarriesNoSignals(t *testing.T) {
	if got := ReadHeaders(headers("Content-Type", "text/html"), agent).Signals(); got != nil {
		t.Errorf("a page that said nothing recorded %v", got)
	}
}

// The column and the evidence have to agree, because the contract rejects a
// document that carries reservations it read and a consent state that does not
// follow from them.
func TestTheColumnAndTheEvidenceGoIntoTheRecordTogether(t *testing.T) {
	for _, tt := range []struct {
		line    string
		consent doc.Consent
	}{
		{"noai", doc.ConsentNoTrain},
		{"noindex", doc.ConsentNoIndex},
		{"nosnippet", doc.ConsentOpen},
	} {
		r := ReadHeaders(headers("X-Robots-Tag", tt.line), agent)
		var d doc.Document
		d.TDMSignals, d.Consent = r.Signals(), r.Consent()
		if d.Consent != tt.consent {
			t.Errorf("%q became %q and should be %q", tt.line, d.Consent, tt.consent)
		}
		if len(d.TDMSignals) == 0 {
			t.Errorf("%q left no evidence behind", tt.line)
		}
		// A fetch that asked is never unasked, which is the state the
		// contract reads as nobody having looked.
		if d.Consent == doc.ConsentUnasked {
			t.Errorf("%q was read and came back as unasked", tt.line)
		}
	}
}
