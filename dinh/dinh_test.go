package dinh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// page is one scanned page that came back the way a scanned page comes back.
func page(doc string, n int, route string) Page {
	return Page{
		Document: doc,
		Page:     n,
		Route:    route,
		Image:    fmt.Sprintf("gao-pdf/2026-09/%s/%04d.jpg", doc, n),
		Bytes:    880_000 + int64(n%7)*40_000,
		Hash:     fmt.Sprintf("sha256:%s-%04d", doc, n),
		DPI:      300,
		Chars:    2_400 + n%11*90,
		Ink:      0.062,
		Stored:   true,
	}
}

// scanned builds a batch of docs documents of pages each, on the route that
// makes the renders, which is the expensive one and the only one that produces
// page images nobody had to ask for.
func scanned(docs, pages int) Batch {
	b := Batch{Name: "gao-pdf-2026-09"}
	for d := range docs {
		name := fmt.Sprintf("vbpl-2019-%03d", d)
		for n := 1; n <= pages; n++ {
			b.Pages = append(b.Pages, page(name, n, OCR))
		}
	}
	return b
}

func refuses(t *testing.T, b Batch, want string) {
	t.Helper()
	why := b.Blocking()
	if len(why) == 0 {
		t.Fatalf("the batch was accepted and it should have been refused for %q", want)
	}
	for _, w := range why {
		if strings.Contains(w, want) {
			return
		}
	}
	t.Errorf("no refusal mentions %q, and what came back was:\n  %s", want, strings.Join(why, "\n  "))
}

func TestABatchOfPairsIsAnAlignedSetRatherThanAPileOfImages(t *testing.T) {
	b := scanned(20, 12)
	if !b.Settled() {
		t.Fatalf("a clean batch was refused: %v", b.Blocking())
	}
	if !b.Holds(0) {
		t.Fatalf("a clean batch does not hold: %s", b.Verdict(0))
	}
	if b.Paired() != len(b.Pages) {
		t.Errorf("%d of %d pages are pairs", b.Paired(), len(b.Pages))
	}
	if got := len(b.Documents()); got != 20 {
		t.Errorf("%d documents came off a batch of 20", got)
	}
	if v := b.Verdict(0); !strings.Contains(v, "the pairs are what the vision work later reads") {
		t.Errorf("the verdict does not say what the pairs are for:\n  %s", v)
	}
}

func TestAPageMissingFromTheMiddleIsReportedRatherThanClosedUp(t *testing.T) {
	b := scanned(3, 9)
	// Page 4 of the second document never rendered, which is the failure that
	// shifts every pair after it if anybody renumbers around it.
	var kept []Page
	for _, p := range b.Pages {
		if p.Document == "vbpl-2019-001" && p.Page == 4 {
			continue
		}
		kept = append(kept, p)
	}
	b.Pages = kept
	refuses(t, b, "vbpl-2019-001 runs to page 9 and is missing page 4")

	twice := scanned(2, 6)
	twice.Pages = append(twice.Pages, page("vbpl-2019-000", 3, OCR))
	refuses(t, twice, "carries page 3 twice")
}

func TestTextThatCameOffAnotherPageIsCaughtByTheInk(t *testing.T) {
	b := scanned(4, 8)
	// A page with nothing on it that produced two thousand characters is the pair
	// being wrong, which is the one failure that has no other symptom.
	b.Pages[9].Ink = 0.0004
	refuses(t, b, "so the text came off some other page")

	// The same page with no text on it either is a blank page, which is a normal
	// thing for a scanned document to contain.
	b.Pages[9].Chars = 0
	if !b.Settled() {
		t.Errorf("a blank page was refused: %v", b.Blocking())
	}
	if b.Blank() != 1 {
		t.Errorf("%d pages read as blank and one is", b.Blank())
	}
	if b.Pages[9].Lost() {
		t.Error("a blank page reads as one the extraction lost")
	}
}

func TestAPageTheExtractionLostIsNotAPair(t *testing.T) {
	b := scanned(10, 10)
	for i := range 6 {
		b.Pages[i*7].Chars = 3 // ink on the page, three characters off it
	}
	if !b.Settled() {
		t.Fatalf("pages the extraction lost were refused rather than counted: %v", b.Blocking())
	}
	if b.Lost() != 6 {
		t.Fatalf("%d pages read as lost and six are", b.Lost())
	}
	if b.Holds(0) {
		t.Errorf("a batch that lost %s of itself holds", percent(b.Dropped()))
	}
	if v := b.Verdict(0); !strings.Contains(v, "a report about the extraction rather than a batch") {
		t.Errorf("the verdict does not say what a lossy batch is:\n  %s", v)
	}
}

func TestARenderTooSmallToCarryToneMarksIsRefused(t *testing.T) {
	b := scanned(3, 6)
	b.Pages[4].DPI = 150
	refuses(t, b, "under the 200 where Vietnamese tone marks survive")
	if b.Pages[4].Paired() {
		t.Error("a page rendered at 150 dpi reads as training data")
	}
}

func TestAnImageNobodyHashedCannotBeCheckedAfterItMoves(t *testing.T) {
	b := scanned(3, 6)
	b.Pages[2].Hash = ""
	refuses(t, b, "an image nobody hashed")

	sizeless := scanned(3, 6)
	sizeless.Pages[2].Bytes = 0
	refuses(t, sizeless, "an image path and no bytes behind it")
}

func TestABatchThatStaysOnTheBoxIsRefusedBeforeTheDiskFills(t *testing.T) {
	b := scanned(200, 40)
	for i := range b.Pages {
		b.Pages[i].Stored = false
	}
	if b.Resident() != b.Bytes() {
		t.Fatalf("%d bytes read as resident out of %d", b.Resident(), b.Bytes())
	}
	// 8,000 pages at about 900 KB is around 7 GB, which fits the project window
	// and does not fit a box with two gigabytes left on it.
	if !b.Fits(0) {
		t.Errorf("%s did not fit the %s window", bytes(b.Resident()), bytes(MaxResident))
	}
	if b.Fits(2_000_000_000) {
		t.Errorf("%s fit on a box with 2.0 GB free", bytes(b.Resident()))
	}
	if b.Holds(2_000_000_000) {
		t.Error("a batch that does not fit the box it ran on holds")
	}
	v := b.Verdict(2_000_000_000)
	if !strings.Contains(v, "a disk that is full rather than a queue that is long") {
		t.Errorf("the verdict does not say what a full box costs:\n  %s", v)
	}
}

func TestWhatReachedTheStoreIsWhatCanBeDeletedLocally(t *testing.T) {
	b := scanned(10, 10)
	for i := range b.Pages {
		b.Pages[i].Stored = i%4 != 0
	}
	if b.Stored()+b.Resident() != b.Bytes() {
		t.Errorf("%d stored and %d resident do not add to %d", b.Stored(), b.Resident(), b.Bytes())
	}
	if b.Resident() >= b.Stored() {
		t.Errorf("a quarter of the batch is %d bytes and three quarters is %d", b.Resident(), b.Stored())
	}
	if v := b.Verdict(0); !strings.Contains(v, "reached the store and") {
		t.Errorf("the verdict does not report the split:\n  %s", v)
	}
}

func TestTheRoutesAreReportedApartBecauseOnlyOneOfThemRenders(t *testing.T) {
	b := scanned(6, 10)
	for i := range b.Pages {
		switch {
		case i < 24:
			b.Pages[i].Route = Direct
		case i < 33:
			b.Pages[i].Route = Legacy
		}
	}
	routes := b.Routes()
	if len(routes) != 3 {
		t.Fatalf("%d routes came back and P04 names three", len(routes))
	}
	var pages int
	for _, r := range routes {
		pages += r.Pages
		if r.Paired != r.Pages {
			t.Errorf("route %s has %d pages and %d of them are pairs", r.Key, r.Pages, r.Paired)
		}
	}
	if pages != len(b.Pages) {
		t.Errorf("the routes hold %d pages and the batch holds %d", pages, len(b.Pages))
	}
	if routes[2].Share <= 0.4 || routes[2].Share >= 0.5 {
		t.Errorf("27 of 60 pages on route O is a share of %.2f", routes[2].Share)
	}
}

func TestABornDigitalPageWithNoRenderIsNotAMissingPair(t *testing.T) {
	// Half the batch is born digital, read out of its text layer, and nothing
	// ever rendered it. Counting those as unattached would turn the attachment
	// figure into a report about the routing.
	b := scanned(10, 10)
	for i := range b.Pages {
		if i < 50 {
			b.Pages[i].Route = Direct
			b.Pages[i].Image, b.Pages[i].Hash, b.Pages[i].Bytes, b.Pages[i].DPI = "", "", 0, 0
		}
	}
	if !b.Settled() {
		t.Fatalf("a mixed batch was refused: %v", b.Blocking())
	}
	if b.Rendered() != 50 {
		t.Fatalf("%d pages read as rendered and fifty were", b.Rendered())
	}
	if b.Attached() != 1 {
		t.Errorf("the attachment share is %.2f and every rendered page is a pair", b.Attached())
	}
	if !b.Holds(0) {
		t.Errorf("a mixed batch does not hold: %s", b.Verdict(0))
	}

	// A scanned page with no render is a different thing. Something read it and
	// there is nothing to check what it read.
	b.Pages[70].Image, b.Pages[70].Hash = "", ""
	refuses(t, b, "took the OCR route and carries no image")
}

func TestABatchNobodyRenderedHasNoPairsInIt(t *testing.T) {
	b := scanned(4, 5)
	for i := range b.Pages {
		b.Pages[i].Route = Direct
		b.Pages[i].Image, b.Pages[i].Hash, b.Pages[i].Bytes, b.Pages[i].DPI = "", "", 0, 0
	}
	refuses(t, b, "no page in this batch was rendered")
}

func TestAPageWithNoDocumentOrNoRouteIsNotJoinedToAnything(t *testing.T) {
	loose := scanned(2, 4)
	loose.Pages[3].Document = ""
	refuses(t, loose, "cannot be joined to anything")

	misrouted := scanned(2, 4)
	misrouted.Pages[3].Route = "ocr"
	refuses(t, misrouted, `took route "ocr"`)

	unnumbered := scanned(2, 4)
	unnumbered.Pages[3].Page = 0
	refuses(t, unnumbered, "numbered from zero or below")
}

func TestAnEmptyBatchReachesNoVerdictAboutAnything(t *testing.T) {
	var b Batch
	refuses(t, b, "no pages were read")
	if b.Holds(0) || b.Attached() != 0 || b.Paired() != 0 {
		t.Error("an empty batch reported on a set of pages")
	}
	if b.Verdict(0) != b.Blocking()[0] {
		t.Error("the verdict does not lead with the reason there is nothing to report")
	}
}

func TestABatchThatBrokeEverywhereIsReportedOnceRatherThanPerPage(t *testing.T) {
	// A render that came out at 96 dpi came out at 96 dpi on all four hundred
	// pages, and four hundred identical lines are a way of hiding the next fault.
	b := scanned(40, 10)
	for i := range b.Pages {
		b.Pages[i].DPI = 96
	}
	why := b.Blocking()
	if len(why) > 10 {
		t.Fatalf("%d lines came back off one repeated fault", len(why))
	}
	if !strings.Contains(strings.Join(why, "\n"), "like the ones above") {
		t.Errorf("the rest of the faults were dropped rather than counted:\n  %s", strings.Join(why, "\n  "))
	}
}

func TestReadingABatchOffDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pages.jsonl")
	lines := make([]string, 0, 24)
	for n := 1; n <= 24; n++ {
		lines = append(lines, fmt.Sprintf(
			`{"document":"cong-bao-2018-07","page":%d,"route":"O","image":"gao-pdf/2026-09/cong-bao-2018-07/%04d.jpg","bytes":912000,"hash":"sha256:%04d","dpi":300,"chars":2610,"ink":0.058,"stored":true}`,
			n, n, n))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := ReadBatch("gao-pdf-2026-09", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Pages) != 24 {
		t.Fatalf("%d pages came off a file holding 24", len(b.Pages))
	}
	if !b.Holds(0) {
		t.Errorf("a clean batch does not hold: %s", b.Verdict(0))
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"document":"x","page":1,"width":2480}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBatch("x", bad); err == nil {
		t.Error("a page carrying an undeclared column was read")
	}
	if _, err := ReadBatch("x", filepath.Join(dir, "nothing.jsonl")); err == nil {
		t.Error("a file that is not there read as a batch")
	}
}
