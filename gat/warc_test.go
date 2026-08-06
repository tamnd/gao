package gat_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tamnd/gao/gat"
)

// A WARC is the thing that makes an extraction bug fixable. Every test here is
// some version of the same question: if the crawl is over and the sites have
// changed, is what we kept enough.

func when() time.Time {
	return time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)
}

func visit() *gat.Visit {
	return &gat.Visit{
		URL:    "https://vnexpress.net/tin-tuc/bai-viet-4712345.html",
		Host:   "vnexpress.net",
		Status: 200,
		Header: http.Header{
			"Content-Type":   {"text/html; charset=utf-8"},
			"Content-Length": {"58"},
			"Server":         {"nginx"},
		},
		Body:   []byte("<html><body><p>Giá lúa tăng mạnh ở đồng bằng.</p></body></html>"),
		Robots: gat.Decision{Allowed: true, Why: gat.RobotsAllow, Rule: "Allow: /tin-tuc"},
	}
}

func write(t *testing.T, gz bool, records ...*gat.Record) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gat.NewWARCWriter(&buf, gz)
	for _, r := range records {
		if _, _, err := w.Write(r); err != nil {
			t.Fatalf("writing a %s record: %v", r.Type(), err)
		}
	}
	return buf.Bytes()
}

func read(t *testing.T, b []byte) []*gat.Record {
	t.Helper()
	r, err := gat.NewWARCReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("opening: %v", err)
	}
	var out []*gat.Record
	for {
		rec, err := r.Next()
		if errors.Is(err, gat.ErrDone) {
			return out
		}
		if err != nil {
			t.Fatalf("reading record %d: %v", len(out), err)
		}
		out = append(out, rec)
	}
}

// The whole claim of the format, tested end to end. A page goes in, the crawl
// is over, and the page comes back out with its bytes and its headers.
func TestAPageComesBackOutWithoutRefetchingIt(t *testing.T) {
	v := visit()
	for _, gz := range []bool{false, true} {
		records := read(t, write(t, gz, gat.VisitRecords(v, when(), "gaobot/1.0")...))
		if len(records) != 2 {
			t.Fatalf("gz=%v: %d records, want a request and a response", gz, len(records))
		}

		resp, err := records[1].Response()
		if err != nil {
			t.Fatalf("gz=%v: %v", gz, err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("gz=%v: status %d", gz, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("gz=%v: content type %q", gz, got)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("gz=%v: %v", gz, err)
		}
		if !bytes.Equal(body, v.Body) {
			t.Errorf("gz=%v: the body came back as %q", gz, body)
		}
	}
}

// Vietnamese is the corpus, so the bytes that carry it are the bytes that have
// to survive. A length counted in characters rather than bytes would truncate
// every page in the crawl, and it would truncate them by a different amount
// depending on how many tone marks were on the page.
func TestVietnameseSurvivesTheRoundTripByteForByte(t *testing.T) {
	v := visit()
	v.Body = []byte("Cộng hòa xã hội chủ nghĩa Việt Nam. Độc lập, Tự do, Hạnh phúc. Đường Nguyễn Huệ, Quận 1, Thành phố Hồ Chí Minh.")

	records := read(t, write(t, true, gat.VisitRecords(v, when(), "gaobot/1.0")...))
	resp, err := records[1].Response()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, v.Body) {
		t.Errorf("the page came back as %q", body)
	}
}

// Two records in a row have to be tellable apart by a reader that does not know
// how long either of them is, which is the only reason the two CRLFs exist.
func TestManyRecordsInOneFile(t *testing.T) {
	records := make([]*gat.Record, 0, 8)
	records = append(records, gat.Info(gat.WARCInfo{
		Filename:  "gao-crawl-2026-03-15-00000.warc.gz",
		Software:  "gao/1.0",
		Agent:     "gaobot/1.0",
		Operator:  "gao",
		Contact:   "https://github.com/tamnd/gao/blob/main/LIEN-HE.md",
		Described: when(),
	}))
	for i, path := range []string{"/a", "/b", "/c"} {
		v := visit()
		v.URL = "https://vnexpress.net" + path
		v.Body = bytes.Repeat([]byte("x"), i*1000)
		records = append(records, gat.VisitRecords(v, when(), "gaobot/1.0")...)
	}

	got := read(t, write(t, true, records...))
	if len(got) != 7 {
		t.Fatalf("%d records, want 7", len(got))
	}
	if got[0].Type() != "warcinfo" {
		t.Errorf("the file does not open with a warcinfo record: %s", got[0].Type())
	}
	for i, want := range []string{"/a", "/a", "/b", "/b", "/c", "/c"} {
		if uri := got[i+1].URI(); !strings.HasSuffix(uri, want) {
			t.Errorf("record %d is for %s, want a URL ending %s", i+1, uri, want)
		}
	}
}

// Every record is gzipped on its own, which is what makes a fifty gigabyte file
// something an index can point into rather than something that has to be read
// from the front.
func TestEveryRecordIsItsOwnGzipMember(t *testing.T) {
	v := visit()
	var buf bytes.Buffer
	w := gat.NewWARCWriter(&buf, true)

	offsets := make([]int64, 0, 2)
	lengths := make([]int64, 0, 2)
	for _, r := range gat.VisitRecords(v, when(), "gaobot/1.0") {
		off, n, err := w.Write(r)
		if err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, off)
		lengths = append(lengths, n)
	}

	// The response record, read on its own out of the middle of the file with
	// nothing in front of it inflated. This is the whole reason for per record
	// compression and it is worth asserting rather than assuming.
	whole := buf.Bytes()
	slice := whole[offsets[1] : offsets[1]+lengths[1]]
	only := read(t, slice)
	if len(only) != 1 {
		t.Fatalf("reading one record out of the middle gave %d", len(only))
	}
	if only[0].Type() != "response" {
		t.Errorf("the record at the offset is a %s", only[0].Type())
	}
	if only[0].URI() != v.URL {
		t.Errorf("it is for %s", only[0].URI())
	}
}

// The same fetch written twice has to produce the same file. `gao kho reproduce`
// rebuilds a snapshot and compares bytes, and a random record id would mean
// every WARC in the corpus fails that comparison for a reason that is not a
// problem, which is worse than not checking.
func TestTheSameCrawlWrittenTwiceIsTheSameBytes(t *testing.T) {
	first := write(t, true, gat.VisitRecords(visit(), when(), "gaobot/1.0")...)
	second := write(t, true, gat.VisitRecords(visit(), when(), "gaobot/1.0")...)
	if !bytes.Equal(first, second) {
		t.Error("two writes of one fetch produced different files")
	}
}

// The other half of that. Two different pages must not land on the same record
// id, or an index keyed on it silently loses one of them.
func TestTwoDifferentPagesGetDifferentIdentities(t *testing.T) {
	a := visit()
	b := visit()
	b.URL = "https://vnexpress.net/tin-tuc/bai-khac.html"

	ra := gat.VisitRecords(a, when(), "gaobot/1.0")[1]
	rb := gat.VisitRecords(b, when(), "gaobot/1.0")[1]

	ida, _ := ra.Get("WARC-Record-ID")
	idb, _ := rb.Get("WARC-Record-ID")
	if ida == idb {
		t.Errorf("two pages share the identity %s", ida)
	}
	if !strings.HasPrefix(ida, "<urn:uuid:") || !strings.HasSuffix(ida, ">") {
		t.Errorf("the identity is not a well formed record id: %s", ida)
	}

	// And a page whose body changed is a different record, since the identity
	// covers the block and not only the fields.
	c := visit()
	c.Body = append(c.Body, '!')
	idc, _ := gat.VisitRecords(c, when(), "gaobot/1.0")[1].Get("WARC-Record-ID")
	if idc == ida {
		t.Error("a page that changed kept its old identity")
	}
}

// The request and the response are one exchange, and a reader has to be able to
// tell that from the file rather than from the fact that they were adjacent.
func TestTheRequestAndTheResponseAreJoined(t *testing.T) {
	records := gat.VisitRecords(visit(), when(), "gaobot/1.0")
	reqID, _ := records[0].Get("WARC-Record-ID")
	link, ok := records[1].Get("WARC-Concurrent-To")
	if !ok {
		t.Fatal("the response does not point at its request")
	}
	if link != reqID {
		t.Errorf("the response points at %s and the request is %s", link, reqID)
	}
}

// What a site said about mining is not on the wire in any form a WARC reader
// knows about, so it has to be written down deliberately or it is lost. A
// consent state nobody can check against the fetch that produced it is a
// consent state nobody has to believe.
func TestWhatTheSiteSaidAboutMiningIsInTheRecord(t *testing.T) {
	v := visit()
	v.Reserve = gat.Reservation{
		NoTrain: true,
		Policy:  "https://vnexpress.net/dieu-khoan",
		Said:    []string{"tdm-reservation: 1", "X-Robots-Tag: noai"},
	}

	rec := read(t, write(t, true, gat.VisitRecords(v, when(), "gaobot/1.0")...))[1]

	said, ok := rec.Get("X-Gao-Reservation-Said")
	if !ok {
		t.Fatal("the record does not say what the site said")
	}
	for _, directive := range v.Reserve.Said {
		if !strings.Contains(said, directive) {
			t.Errorf("%q is not in %q", directive, said)
		}
	}

	// The conclusion is a separate field from the statement, for the same reason
	// they are separate columns in the store: a record holding only the
	// conclusion is a record where the reasoning cannot be checked.
	if got, _ := rec.Get("X-Gao-Reservation"); got != "notrain" {
		t.Errorf("the conclusion reads %q", got)
	}
	if got, _ := rec.Get("X-Gao-Reservation-Policy"); got != v.Reserve.Policy {
		t.Errorf("the policy the site pointed at reads %q", got)
	}
	if got, _ := rec.Get("X-Gao-Robots"); got != "Allow: /tin-tuc" {
		t.Errorf("the rule that allowed the fetch reads %q", got)
	}
}

// A page that reserved nothing and a page nobody asked are different states, and
// the difference is the whole reason the consent column has an empty value in
// it. An absent field says the page reserved nothing. It does not say yes.
func TestAPageThatReservedNothingSaysNothing(t *testing.T) {
	rec := read(t, write(t, true, gat.VisitRecords(visit(), when(), "gaobot/1.0")...))[1]
	if got, ok := rec.Get("X-Gao-Reservation"); ok {
		t.Errorf("a page that reserved nothing carries a reservation of %q", got)
	}
	if _, ok := rec.Get("X-Gao-Reservation-Said"); ok {
		t.Error("a page that said nothing carries a statement")
	}
}

// The digest is what somebody uses to prove a page in the corpus is the page the
// site served, so it has to be over the bytes and it has to say which algorithm
// it is.
func TestTheDigestCoversTheBytes(t *testing.T) {
	a := gat.VisitRecords(visit(), when(), "gaobot/1.0")[1]

	changed := visit()
	changed.Body = append(changed.Body, ' ')
	b := gat.VisitRecords(changed, when(), "gaobot/1.0")[1]

	da, ok := a.Get("WARC-Payload-Digest")
	if !ok {
		t.Fatal("the response carries no payload digest")
	}
	db, _ := b.Get("WARC-Payload-Digest")
	if da == db {
		t.Error("a page with one byte added has the same digest")
	}
	if !strings.HasPrefix(da, "sha256:") {
		t.Errorf("the digest does not name its algorithm: %s", da)
	}
	if block, _ := a.Get("WARC-Block-Digest"); block == da {
		t.Error("the block digest and the payload digest are the same, so one of them covers the wrong thing")
	}
}

// The file has to open with a record that says who made it, because the identity
// we publish in a User-Agent is worth nothing if the file it produced does not
// carry it.
func TestTheFileSaysWhoWroteItAndWhereToComplain(t *testing.T) {
	rec := read(t, write(t, true, gat.Info(gat.WARCInfo{
		Filename:  "gao-crawl-2026-03-15-00000.warc.gz",
		Software:  "gao/1.0",
		Agent:     "gaobot/1.0 (+https://github.com/tamnd/gao)",
		Operator:  "gao",
		Contact:   "https://github.com/tamnd/gao/blob/main/LIEN-HE.md",
		Robots:    "obey",
		IsPartOf:  "gao-crawl-2026-09",
		Described: when(),
	})))[0]

	if rec.Type() != "warcinfo" {
		t.Fatalf("the record is a %s", rec.Type())
	}
	block := string(rec.Block)
	for _, want := range []string{
		"software: gao/1.0",
		"http-header-user-agent: gaobot/1.0",
		"robots-token: gaobot",
		"contact: https://github.com/tamnd/gao/blob/main/LIEN-HE.md",
		"isPartOf: gao-crawl-2026-09",
		"format: WARC file version 1.1",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("%q is not in the warcinfo record:\n%s", want, block)
		}
	}
	if name, _ := rec.Get("WARC-Filename"); name != "gao-crawl-2026-03-15-00000.warc.gz" {
		t.Errorf("the file does not name itself: %q", name)
	}
}

// A truncated file is the ordinary case rather than the exotic one. A crawl gets
// killed, a disk fills, a box is rebooted, and what is left has to fail where it
// stops rather than hand back a record assembled out of two.
func TestATruncatedFileFailsWhereItStops(t *testing.T) {
	whole := write(t, false,
		gat.VisitRecords(visit(), when(), "gaobot/1.0")[1],
		gat.VisitRecords(visit(), when(), "gaobot/1.0")[0],
	)
	cut := whole[:len(whole)-40]

	r, err := gat.NewWARCReader(bytes.NewReader(cut))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); err != nil {
		t.Fatalf("the first whole record did not survive the truncation: %v", err)
	}
	_, err = r.Next()
	if err == nil || errors.Is(err, gat.ErrDone) {
		t.Errorf("a half written record was read as %v rather than as an error", err)
	}
}

// A file that is not a WARC has to say so rather than produce something.
func TestSomethingThatIsNotAWARCSaysSo(t *testing.T) {
	r, err := gat.NewWARCReader(strings.NewReader("GET / HTTP/1.1\r\n\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); err == nil {
		t.Error("an HTTP request was read as a WARC record")
	}
}

// An empty file is finished rather than broken, since a crawl that was killed
// before its first fetch leaves one.
func TestAnEmptyFileIsJustFinished(t *testing.T) {
	r, err := gat.NewWARCReader(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Next(); !errors.Is(err, gat.ErrDone) {
		t.Errorf("an empty file read as %v", err)
	}
}

// A field carrying a newline would end the record early and everything after it
// in the file would be read as something else. It is refused rather than
// escaped, because there is no escaping in this format to do it with.
func TestAFieldThatWouldEndTheRecordEarlyIsRefused(t *testing.T) {
	rec := &gat.Record{Block: []byte("x")}
	rec.Set("WARC-Type", "response")
	rec.Set("WARC-Target-URI", "https://vnexpress.net/a\r\nContent-Length: 0")

	var buf bytes.Buffer
	w := gat.NewWARCWriter(&buf, false)
	if _, _, err := w.Write(rec); err == nil {
		t.Error("a field carrying a newline was written")
	}
	if buf.Len() != 0 {
		t.Error("a record that was refused still wrote bytes")
	}
}

// The offset is what an index entry points at, so it has to be the offset of the
// record rather than of whatever came after it.
func TestTheOffsetIsWhereTheRecordStarts(t *testing.T) {
	var buf bytes.Buffer
	w := gat.NewWARCWriter(&buf, true)
	if w.Offset() != 0 {
		t.Errorf("an empty file starts at %d", w.Offset())
	}
	records := gat.VisitRecords(visit(), when(), "gaobot/1.0")
	off1, n1, _ := w.Write(records[0])
	off2, n2, _ := w.Write(records[1])

	if off1 != 0 {
		t.Errorf("the first record is at %d", off1)
	}
	if off2 != n1 {
		t.Errorf("the second record is at %d and the first is %d long", off2, n1)
	}
	if w.Offset() != n1+n2 {
		t.Errorf("the file is %d bytes and the records are %d and %d", w.Offset(), n1, n2)
	}
	if int64(buf.Len()) != n1+n2 {
		t.Errorf("the writer counted %d bytes and wrote %d", n1+n2, buf.Len())
	}
}

// A redirect is handed back to the frontier rather than followed, and where it
// pointed is part of what happened to that URL.
func TestWhereARedirectPointedIsKept(t *testing.T) {
	v := visit()
	v.Status = 301
	v.Body = nil
	v.Redirect = "https://vnexpress.net/tin-tuc/moi"

	rec := read(t, write(t, true, gat.VisitRecords(v, when(), "gaobot/1.0")...))[1]
	if got, _ := rec.Get("X-Gao-Redirect"); got != v.Redirect {
		t.Errorf("the redirect target reads %q", got)
	}
	resp, err := rec.Response()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("the status came back as %d", resp.StatusCode)
	}
}

// The one that has to be right or every compressed page in the crawl comes back
// short. The transport hands us a body it has already decompressed, so the
// Content-Length the site sent counts bytes we no longer have, and copying it
// through means every reader in the world truncates our page to the compressed
// length without saying anything.
func TestALengthFromTheWireDoesNotTruncateThePage(t *testing.T) {
	v := visit()
	v.Header.Set("Content-Length", "12")
	v.Header.Set("Content-Encoding", "gzip")

	rec := read(t, write(t, true, gat.VisitRecords(v, when(), "gaobot/1.0")...))[1]
	resp, err := rec.Response()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, v.Body) {
		t.Errorf("the page came back as %d bytes of %d: %q", len(body), len(v.Body), body)
	}

	// A Content-Encoding beside bytes that are not encoded that way is a lie a
	// parser will act on, so it does not go in the HTTP block either.
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("the response still claims to be %s encoded", got)
	}

	// What the site sent is still part of what happened, so it is kept where it
	// is a record rather than an instruction.
	if got, _ := rec.Get("X-Gao-Sent-Content-Length"); got != "12" {
		t.Errorf("the length the site sent reads %q", got)
	}
	if got, _ := rec.Get("X-Gao-Sent-Content-Encoding"); got != "gzip" {
		t.Errorf("the encoding the site sent reads %q", got)
	}
}
