package gat

// Writing down what came back, in the format everybody else already reads.
//
// A crawl that keeps only the text it extracted has thrown away the page. Every
// extraction bug found afterwards is then a bug that can only be fixed by
// fetching 700 million pages again, from sites that have changed, some of which
// are gone. WARC is the answer the web archiving world settled on twenty years
// ago and it is a standard rather than a format we invented: ISO 28500, read by
// warcio, by wget, by every tool at the Internet Archive, and by whoever wants
// to check our corpus against the pages it came from.
//
// The record we write is the response as it arrived plus the request that asked
// for it, and one thing that is not on the wire at all: what the site said about
// mining. A reservation that was read at fetch time and not written down is a
// reservation nobody can check later, and the whole reason the consent state is
// a column rather than a setting is that somebody downstream has to be able to
// audit it.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tamnd/gao/doc"
)

// The version we write. WARC 1.1 differs from 1.0 in ways that matter to
// somebody reading this in ten years: WARC-Date carries sub second precision and
// the revisit profiles are named properly. Everything that reads 1.0 reads 1.1.
const warcVersion = "WARC/1.1"

// warcTime is the date format the standard requires, which is not RFC 3339 by
// accident: it is RFC 3339 with the zone fixed at Z, because a crawl log in
// local time is a crawl log that cannot be merged with anybody else's.
const warcTime = "2006-01-02T15:04:05Z"

// A Record is one WARC record: a set of named fields and a block of bytes.
//
// Fields are held as a slice rather than a map because WARC field order is part
// of what a byte for byte rebuild has to reproduce, and a map would hand us a
// different order on every run. Nothing else in this file sorts them.
type Record struct {
	Fields []Field
	Block  []byte
}

// A Field is one WARC named field. The name is written as given, since the
// standard says names are case insensitive and every tool in the world still
// prints them the way they were written.
type Field struct {
	Name  string
	Value string
}

// Get returns the first value of a named field, case insensitively, and whether
// it was there at all. A field that is absent and a field that is empty are
// different things in a format where an empty value is legal.
func (r *Record) Get(name string) (string, bool) {
	for _, f := range r.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value, true
		}
	}
	return "", false
}

// Set replaces a field, or appends it when it is not already there. Appending
// rather than inserting keeps the order stable, which is what the rebuild rests
// on.
func (r *Record) Set(name, value string) {
	for i, f := range r.Fields {
		if strings.EqualFold(f.Name, name) {
			r.Fields[i].Value = value
			return
		}
	}
	r.Fields = append(r.Fields, Field{name, value})
}

// Type is the WARC-Type of the record: warcinfo, request, response.
func (r *Record) Type() string {
	t, _ := r.Get("WARC-Type")
	return t
}

// URI is the WARC-Target-URI, which is empty on a warcinfo record and set on
// everything else.
func (r *Record) URI() string {
	u, _ := r.Get("WARC-Target-URI")
	return u
}

// A WARCWriter writes records to a stream, one gzip member each.
//
// Per record compression rather than one stream is what makes a WARC seekable:
// an index can name a byte offset and a length, and a reader can jump to one
// page out of a fifty gigabyte file without inflating the forty nine gigabytes
// in front of it. It costs a few percent of compression ratio and it is the
// difference between an archive and a tape.
type WARCWriter struct {
	w  io.Writer
	n  int64
	gz bool
}

// NewWARCWriter returns a writer over w. Pass gz false only for tests and for
// looking at a file by eye, since an uncompressed WARC is around four times the
// size and the fleet does not have the disk.
func NewWARCWriter(w io.Writer, gz bool) *WARCWriter {
	return &WARCWriter{w: w, gz: gz}
}

// Offset is how many bytes have been written, which is the number an index
// entry points at. It is read between records, and the value is the start of the
// record about to be written.
func (w *WARCWriter) Offset() int64 { return w.n }

// Write writes one record and returns its offset and length in the file, which
// together are the whole of what a CDX index holds about where a page lives.
func (w *WARCWriter) Write(r *Record) (offset, length int64, err error) {
	var head bytes.Buffer
	head.WriteString(warcVersion + "\r\n")
	for _, f := range r.Fields {
		if strings.EqualFold(f.Name, "Content-Length") {
			continue
		}
		if strings.ContainsAny(f.Value, "\r\n") {
			return 0, 0, fmt.Errorf("gat: WARC field %s carries a newline, which would end the record early", f.Name)
		}
		head.WriteString(f.Name + ": " + f.Value + "\r\n")
	}
	// Content-Length is written last and computed here rather than taken from
	// the caller. A record whose length field disagrees with its block is a file
	// every reader after it will misparse, and it is the one field that must
	// never be somebody's arithmetic.
	head.WriteString("Content-Length: " + strconv.Itoa(len(r.Block)) + "\r\n\r\n")

	body := append(head.Bytes(), r.Block...)
	// Two CRLFs close a record. They are not part of Content-Length and every
	// reader relies on them to resynchronize.
	body = append(body, '\r', '\n', '\r', '\n')

	out := body
	if w.gz {
		var buf bytes.Buffer
		// The deflate level and an empty header are both pinned, because a
		// snapshot that rebuilds to different bytes is a snapshot nobody can
		// verify. Go writes no modification time and no name unless asked, so
		// the same record compresses to the same bytes on every machine.
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			return 0, 0, err
		}
		if _, err := zw.Write(body); err != nil {
			return 0, 0, err
		}
		if err := zw.Close(); err != nil {
			return 0, 0, err
		}
		out = buf.Bytes()
	}

	offset = w.n
	n, err := w.w.Write(out)
	w.n += int64(n)
	return offset, int64(n), err
}

// WARCInfo is what goes at the top of every file: who wrote it, with what, and
// under what policy.
//
// It is the part of a WARC that answers a question asked years later by somebody
// who was not here. Software and format are conventional. The operator and the
// contact are not optional for us, because the identity we publish in the
// User-Agent is worth nothing if the file it produced does not carry it.
type WARCInfo struct {
	Filename  string
	Software  string
	Agent     string
	Operator  string
	Contact   string
	Robots    string
	IsPartOf  string
	Described time.Time
}

// Info builds the warcinfo record that opens a file.
func Info(i WARCInfo) *Record {
	var b strings.Builder
	write := func(name, value string) {
		if value != "" {
			b.WriteString(name + ": " + value + "\r\n")
		}
	}
	write("software", i.Software)
	write("operator", i.Operator)
	write("http-header-user-agent", i.Agent)
	write("robots-token", Bot)
	write("isPartOf", i.IsPartOf)
	write("robots", i.Robots)
	write("contact", i.Contact)
	write("format", "WARC file version 1.1")

	r := &Record{Block: []byte(b.String())}
	r.Set("WARC-Type", "warcinfo")
	r.Set("WARC-Date", stamp(i.Described))
	if i.Filename != "" {
		r.Set("WARC-Filename", i.Filename)
	}
	r.Set("Content-Type", "application/warc-fields")
	finish(r)
	return r
}

// Visit turns one fetched page into the pair of records that describe it: the
// request we sent and the response that came back.
//
// The two are joined by WARC-Concurrent-To, which is how a reader knows they are
// one exchange rather than two things that happened to be next to each other.
// The request goes first, because that is the order they happened in and because
// a reader recovering from a damaged file wants the URL before the body.
func VisitRecords(v *Visit, when time.Time, agent string) []*Record {
	req := &Record{Block: requestBlock(v, agent)}
	req.Set("WARC-Type", "request")
	req.Set("WARC-Date", stamp(when))
	req.Set("WARC-Target-URI", v.URL)
	req.Set("Content-Type", "application/http;msgtype=request")
	finish(req)

	resp := &Record{Block: responseBlock(v)}
	resp.Set("WARC-Type", "response")
	resp.Set("WARC-Date", stamp(when))
	resp.Set("WARC-Target-URI", v.URL)
	resp.Set("Content-Type", "application/http;msgtype=response")
	resp.Set("WARC-Payload-Digest", digest(v.Body))
	resp.Set("WARC-Identified-Payload-Type", contentType(v.Header))
	for _, name := range framing {
		if value := v.Header.Get(name); value != "" {
			resp.Set("X-Gao-Sent-"+name, value)
		}
	}

	// The reservation and the robots decision are ours rather than the
	// standard's, prefixed so that no future version of WARC can collide with
	// them. They are written because a fetch that read what a site said about
	// mining and did not record it has left the audit to somebody's memory, and
	// the whole point of the consent column is that it can be checked against
	// something.
	//
	// The statement and the conclusion go in separate fields for the same reason
	// they are separate columns in the store. `tdm-reservation: 1` is what
	// happened and `notrain` is what we decided it meant, and a record holding
	// only the second is a record where the reasoning cannot be checked.
	if said := strings.Join(v.Reserve.Said, "; "); said != "" {
		resp.Set("X-Gao-Reservation-Said", said)
	}
	if note := describeReserve(v.Reserve); note != "" {
		resp.Set("X-Gao-Reservation", note)
	}
	if v.Reserve.Policy != "" {
		resp.Set("X-Gao-Reservation-Policy", v.Reserve.Policy)
	}
	if v.Robots.Rule != "" {
		resp.Set("X-Gao-Robots", v.Robots.Rule)
	}
	if v.Redirect != "" {
		resp.Set("X-Gao-Redirect", v.Redirect)
	}
	finish(resp)

	id, _ := req.Get("WARC-Record-ID")
	resp.Set("WARC-Concurrent-To", id)
	// Concurrent-To changed the field set, so the record identity is taken
	// again. An identity computed over a subset of the record is an identity
	// two different records can share.
	resp.Set("WARC-Record-ID", "")
	finish(resp)

	return []*Record{req, resp}
}

// finish fills in the fields that are computed from everything else: the block
// digest and the record id.
//
// The id is derived from the record rather than drawn at random, which is a
// deliberate departure from what most crawlers do. A random uuid means the same
// crawl written twice produces two files that differ in every record, and `gao
// kho reproduce` exists precisely to rebuild bytes and compare them. Derived
// from the content, an identical fetch written again is an identical file, and
// two records with the same id are the same record.
func finish(r *Record) {
	r.Set("WARC-Block-Digest", digest(r.Block))

	var h strings.Builder
	for _, f := range r.Fields {
		if strings.EqualFold(f.Name, "WARC-Record-ID") {
			continue
		}
		h.WriteString(f.Name)
		h.WriteString("\x00")
		h.WriteString(f.Value)
		h.WriteString("\x00")
	}
	sum := doc.Sum(append([]byte(h.String()), r.Block...))
	r.Set("WARC-Record-ID", urnUUID(sum))
}

// urnUUID formats the first sixteen bytes of a hash as a uuid, with the version
// and variant bits set so that it is a well formed uuid rather than sixteen
// bytes with dashes in. Version 8 is the one the standard set aside for exactly
// this: an identifier whose bits mean something to whoever generated them.
func urnUUID(h doc.Hash) string {
	var b [16]byte
	copy(b[:], h[:16])
	b[6] = (b[6] & 0x0f) | 0x80 // version 8
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	s := hex.EncodeToString(b[:])
	return "<urn:uuid:" + s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32] + ">"
}

// digest is the block digest in the shape the standard asks for: an algorithm
// name, a colon, and the digest in base32.
//
// sha256 rather than the sha1 most WARC files carry. The digest is what somebody
// uses to prove a page in our corpus is the page the site served, and a proof
// resting on sha1 is a proof with a published collision attack against it. The
// label says which it is, so a reader that only knows sha1 skips it rather than
// verifying it wrongly.
func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
}

func stamp(t time.Time) string { return t.UTC().Format(warcTime) }

// requestBlock reconstructs the request we sent.
//
// It is a reconstruction rather than a capture, and that is worth saying plainly
// rather than leaving for somebody to discover. What the crawler sends is one
// header and a GET, so there is nothing here that a capture would have caught
// and this does not, but a reader is entitled to know that these bytes were
// assembled from what we know we send rather than read off the socket.
func requestBlock(v *Visit, agent string) []byte {
	target := "/"
	if i := strings.Index(v.URL, "//"); i >= 0 {
		if j := strings.Index(v.URL[i+2:], "/"); j >= 0 {
			target = v.URL[i+2+j:]
		}
	}
	var b strings.Builder
	b.WriteString("GET " + target + " HTTP/1.1\r\n")
	b.WriteString("Host: " + v.Host + "\r\n")
	b.WriteString("User-Agent: " + agent + "\r\n")
	b.WriteString("\r\n")
	return []byte(b.String())
}

// framing are the response headers that describe how the body arrived rather
// than what it is.
//
// They are the three that cannot be copied through. The transport hands us a
// body that has already been decompressed and dechunked, so a Content-Encoding
// of gzip beside bytes that are not gzip is a lie, and a Content-Length counted
// over the compressed body is a number every reader in the world will truncate
// our page to. What the site sent is kept, in WARC fields where it is a record
// of the exchange rather than an instruction to a parser.
var framing = []string{"Content-Length", "Content-Encoding", "Transfer-Encoding"}

// responseBlock writes the response as an HTTP message.
//
// The header block is rebuilt from the parsed response rather than kept as it
// came off the wire, so the values and the status are exact and the original
// order and letter casing are not. That is a real limitation and it is stated
// here rather than left for somebody to find: a reader comparing our WARC
// against a live refetch will find the same headers with the same values in a
// different order. The names are sorted so that the same response always writes
// the same bytes, which is what a rebuild needs.
func responseBlock(v *Visit) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "HTTP/1.1 %d %s\r\n", v.Status, http.StatusText(v.Status))

	names := make([]string, 0, len(v.Header))
	for name := range v.Header {
		if isFraming(name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, value := range v.Header[name] {
			b.WriteString(name + ": " + value + "\r\n")
		}
	}
	// Written last and computed here for the same reason the WARC one is: a
	// length that disagrees with the bytes after it is a page every reader
	// truncates, and it truncates it silently.
	fmt.Fprintf(&b, "Content-Length: %d\r\n", len(v.Body))
	b.WriteString("\r\n")
	b.Write(v.Body)
	return b.Bytes()
}

func isFraming(name string) bool {
	for _, f := range framing {
		if strings.EqualFold(name, f) {
			return true
		}
	}
	return false
}

func contentType(h http.Header) string {
	ct := h.Get("Content-Type")
	if ct == "" {
		return "application/octet-stream"
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

// describeReserve writes the conclusion drawn from a reservation, and nothing
// when there is no conclusion to draw.
//
// Empty is not the same as open. It means the page reserved nothing, and a page
// that reserved nothing has to stay tellable apart from a page nobody asked,
// which is why the absence of this field is meaningful and its presence with an
// empty value would not be.
func describeReserve(r Reservation) string {
	var parts []string
	if r.NoIndex {
		parts = append(parts, "noindex")
	}
	if r.NoTrain {
		parts = append(parts, "notrain")
	}
	return strings.Join(parts, ",")
}

// A WARCReader reads records back, which is the half of this that makes the
// other half worth anything. A format we can write and not read is a format we
// are trusting somebody else's tool to have understood.
type WARCReader struct {
	br *bufio.Reader
	zr *gzip.Reader
	// gz is decided once from the first two bytes rather than from the file
	// name, since a .warc that is gzipped and a .warc.gz that is not are both
	// things that happen.
	gz    bool
	first bool
}

// NewWARCReader returns a reader over r, compressed or not.
func NewWARCReader(r io.Reader) (*WARCReader, error) {
	br := bufio.NewReader(r)
	magic, err := br.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	w := &WARCReader{br: br, first: true}
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		w.gz = true
	}
	return w, nil
}

// ErrDone is returned by Next when the file is finished.
var ErrDone = errors.New("gat: no more WARC records")

// Next reads the next record.
func (w *WARCReader) Next() (*Record, error) {
	src, err := w.member()
	if err != nil {
		return nil, err
	}
	tp := textproto.NewReader(bufio.NewReader(src))

	version, err := tp.ReadLine()
	if errors.Is(err, io.EOF) {
		return nil, ErrDone
	}
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(version, "WARC/") {
		return nil, fmt.Errorf("gat: expected a WARC record and found %q", clip(version))
	}

	r := &Record{}
	length := -1
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return nil, err
		}
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("gat: WARC field without a colon: %q", clip(line))
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if strings.EqualFold(name, "Content-Length") {
			length, err = strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("gat: WARC Content-Length %q: %w", value, err)
			}
			continue
		}
		r.Fields = append(r.Fields, Field{name, value})
	}
	if length < 0 {
		return nil, errors.New("gat: WARC record with no Content-Length")
	}

	r.Block = make([]byte, length)
	if _, err := io.ReadFull(tp.R, r.Block); err != nil {
		return nil, fmt.Errorf("gat: WARC block of %d bytes: %w", length, err)
	}
	// The two CRLFs after the block. A file that is short here is truncated, and
	// saying so is better than handing back a record and failing on the next
	// one for a reason that makes no sense.
	var tail [4]byte
	if _, err := io.ReadFull(tp.R, tail[:]); err != nil {
		return nil, fmt.Errorf("gat: WARC record is not closed: %w", err)
	}
	if string(tail[:]) != "\r\n\r\n" {
		return nil, fmt.Errorf("gat: WARC record ends with %q rather than two CRLFs", clip(string(tail[:])))
	}
	return r, nil
}

// member hands back a reader over the next gzip member, or the plain stream.
func (w *WARCReader) member() (io.Reader, error) {
	if !w.gz {
		return w.br, nil
	}
	if w.first {
		zr, err := gzip.NewReader(w.br)
		if errors.Is(err, io.EOF) {
			return nil, ErrDone
		}
		if err != nil {
			return nil, err
		}
		zr.Multistream(false)
		w.zr, w.first = zr, false
		return w.zr, nil
	}
	// Drain whatever is left of the member just read, so that Reset starts on a
	// member boundary rather than in the middle of one.
	if _, err := io.Copy(io.Discard, w.zr); err != nil {
		return nil, err
	}
	if err := w.zr.Reset(w.br); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrDone
		}
		return nil, err
	}
	w.zr.Multistream(false)
	return w.zr, nil
}

// Response reads a response record back as an HTTP response, which is what a
// re-extraction actually wants: the body and the headers, without having to
// know that WARC exists.
func (r *Record) Response() (*http.Response, error) {
	if t := r.Type(); !strings.EqualFold(t, "response") {
		return nil, fmt.Errorf("gat: a %s record does not hold a response", t)
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(r.Block)), nil)
	if err != nil {
		return nil, fmt.Errorf("gat: %s: %w", r.URI(), err)
	}
	return resp, nil
}

func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}
