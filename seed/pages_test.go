package seed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/frontier"
	"github.com/tamnd/gao/store"
)

// hub is enough of the Hub to read a published dataset back out of: the listing
// and the resolve endpoint, and the second answers ranges, because reading one
// column of a part by range is the whole reason this route is affordable.
type hub struct {
	t *testing.T

	mu     sync.Mutex
	files  map[string][]byte
	served int64

	srv *httptest.Server
}

func newHub(t *testing.T) *hub {
	t.Helper()
	h := &hub{t: t, files: map[string][]byte{}}
	h.srv = httptest.NewServer(h)
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/tree/"):
		h.tree(w, r)
	case strings.Contains(r.URL.Path, "/resolve/"):
		h.resolve(w, r)
	default:
		h.t.Errorf("the read asked for %s %s, which is no part of reading a repo", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (h *hub) tree(w http.ResponseWriter, r *http.Request) {
	_, prefix, _ := strings.Cut(r.URL.Path, "/tree/main/")

	h.mu.Lock()
	var paths []string
	for p := range h.files {
		if strings.HasPrefix(p, prefix) {
			paths = append(paths, p)
		}
	}
	h.mu.Unlock()
	sort.Strings(paths)

	entries := make([]map[string]any, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, map[string]any{
			"type": "file", "path": p, "oid": "pointer", "size": 134,
			"lfs": map[string]any{"oid": "sha256", "size": len(h.files[p])},
		})
	}
	_ = json.NewEncoder(w).Encode(entries)
}

func (h *hub) resolve(w http.ResponseWriter, r *http.Request) {
	_, path, _ := strings.Cut(r.URL.Path, "/resolve/main/")
	h.mu.Lock()
	body, ok := h.files[path]
	h.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	var from, to int64
	if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &from, &to); err != nil {
		h.t.Errorf("a part was asked for without a range: %q", r.Header.Get("Range"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	if to >= int64(len(body)) {
		to = int64(len(body)) - 1
	}
	h.mu.Lock()
	h.served += to - from + 1
	h.mu.Unlock()

	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, len(body)))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(body[from : to+1])
}

// put writes a part holding one document per address, under the given source.
func (h *hub) put(source string, part int, addresses ...string) {
	h.t.Helper()
	d, ok := store.Lookup("vitco-clean")
	if !ok {
		h.t.Fatal("the cleaned corpus is not in the dataset table")
	}
	dir := h.t.TempDir()
	rel := fmt.Sprintf("%s/%s/part-%05d-of-%05d%s", store.DataDir, source, part, 1, store.ParquetExt)
	p, err := store.CreatePart(dir, rel, d, store.Stamp{Snapshot: source, Stage: "clean@0.1.0", Box: "server1"})
	if err != nil {
		h.t.Fatalf("CreatePart: %v", err)
	}
	defer p.Abandon()
	for _, address := range addresses {
		if err := p.Append(document(address)); err != nil {
			h.t.Fatalf("Append: %v", err)
		}
	}
	if _, err := p.Close(); err != nil {
		h.t.Fatalf("Close: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		h.t.Fatal(err)
	}
	h.mu.Lock()
	h.files[rel] = body
	h.mu.Unlock()
}

// document is a valid document at the given address. Only the url column is
// read back, but a part will not take a document that fails the contract, and
// writing a real one is what makes the test read a real part.
func document(address string) *doc.Document {
	text := "Bài viết tại " + address + ". Cộng hòa xã hội chủ nghĩa Việt Nam, độc lập tự do hạnh phúc. " +
		"Nội dung của tài liệu này đủ dài để vượt qua ngưỡng tối thiểu của hợp đồng nhập liệu."
	host, _ := hostOf(address)
	d := &doc.Document{
		RawID:         doc.SumString("raw:" + address),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
		Provenance: doc.Provenance{
			Source:          doc.SourceFineWeb2,
			SourceLocator:   "v1.0/vie_Latn/000_00000.parquet@0+4096",
			URL:             address,
			Host:            host,
			FetchedAt:       time.Date(2026, 9, 14, 3, 22, 11, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "go-trafilatura@1.4.0",
			PipelineVersion: "0.1.0",
		},
		Language:  doc.Language{Lang: "vie", LangScore: 0.997, Diacritics: "present"},
		Licensing: doc.Licensing{LicenseClass: doc.LicenseOpen, LicenseEvidence: "cc-by from the source"},
	}
	d.DocID = doc.SumString(d.Text)
	d.NChars = doc.Chars(d.Text)
	d.NSyllables = doc.Syllables(d.Text)
	return d
}

// addresses returns n pages on one host, which is what a real dataset looks
// like: a few hosts contributing a great many documents each.
func addresses(host string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("https://%s/bai-viet-%d.html", host, i)
	}
	return out
}

func read(t *testing.T, p Pages) ([]string, PagesReport) {
	t.Helper()
	var got []string
	report, err := p.Read(t.Context(), func(address string) error {
		got = append(got, address)
		return nil
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return got, report
}

func TestASeedOfPagesComesOutOfThePublishedDataset(t *testing.T) {
	h := newHub(t)
	h.put("fineweb2", 0, addresses("vnexpress.net", 20)...)
	h.put("hplt3", 0, addresses("tuoitre.vn", 20)...)

	got, report := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1})
	if len(got) != 40 {
		t.Fatalf("forty documents in the repo came out as %d addresses", len(got))
	}
	if report.Rows != 40 || report.Kept != 40 || report.Parts != 2 || report.Hosts != 2 {
		t.Errorf("the report is %+v, want 40 rows over 2 parts and 2 hosts", report)
	}
	for _, address := range got {
		if !strings.HasPrefix(address, "https://") {
			t.Fatalf("%q is not an address", address)
		}
	}
}

// The cap is the point of the command. A dataset where one host holds ten
// thousand pages and the next holds three would otherwise seed a crawl with one
// site, and the frontier hands out two URLs per host per batch, so the extra ten
// thousand would be deferred over and over rather than fetched.
func TestAHostContributesItsShareAndNoMore(t *testing.T) {
	h := newHub(t)
	h.put("fineweb2", 0, append(addresses("vnexpress.net", 50), addresses("tuoitre.vn", 3)...)...)

	got, report := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: 8})
	if len(got) != 11 {
		t.Fatalf("a cap of eight over two hosts gave %d addresses, want 11", len(got))
	}
	if report.Capped != 42 {
		t.Errorf("the report says %d addresses were past a host's share, want 42", report.Capped)
	}
	per := map[string]int{}
	for _, address := range got {
		host, _ := hostOf(address)
		per[host]++
	}
	if per["vnexpress.net"] != 8 || per["tuoitre.vn"] != 3 {
		t.Errorf("the hosts contributed %v, want eight and three", per)
	}
}

// The cap is across parts and not within one, because a host appears in more
// than one source and a per part cap would hand it its share once per source.
func TestTheCapHoldsAcrossParts(t *testing.T) {
	h := newHub(t)
	h.put("fineweb2", 0, addresses("vnexpress.net", 20)...)
	h.put("hplt3", 0, addresses("vnexpress.net", 20)...)

	got, _ := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: 5})
	if len(got) != 5 {
		t.Errorf("one host over two parts at a cap of five gave %d addresses, want 5", len(got))
	}
}

// A seed sharded some other way than the crawler shards its frontier is a fleet
// where each box is handed addresses it will refuse, and this is the check that
// the two rules are the one rule.
func TestEachBoxGetsTheHostsItsCrawlerWouldOwn(t *testing.T) {
	const fleet = 3
	h := newHub(t)
	hosts := []string{
		"vnexpress.net", "tuoitre.vn", "thanhnien.vn", "dantri.com.vn", "vietnamnet.vn",
		"cafef.vn", "kenh14.vn", "voz.vn", "tinhte.vn", "genk.vn",
	}
	all := make([]string, 0, len(hosts)*3)
	for _, host := range hosts {
		all = append(all, addresses(host, 3)...)
	}
	h.put("fineweb2", 0, all...)

	seen := map[string]bool{}
	for shard := range fleet {
		got, report := read(t, Pages{
			Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1,
			Shard: shard, Fleet: fleet,
		})
		if report.Kept+report.Foreign != len(all) {
			t.Errorf("box %d kept %d and passed over %d of %d addresses", shard, report.Kept, report.Foreign, len(all))
		}
		for _, address := range got {
			host, _ := hostOf(address)
			if want := frontier.Box(host, fleet); want != shard {
				t.Errorf("box %d was handed %s, which box %d owns", shard, host, want)
			}
			if seen[address] {
				t.Errorf("%s went to two boxes", address)
			}
			seen[address] = true
		}
	}
	if len(seen) != len(all) {
		t.Errorf("the three boxes between them got %d of %d addresses", len(seen), len(all))
	}
}

func TestAReadStopsAtTheLimit(t *testing.T) {
	h := newHub(t)
	h.put("fineweb2", 0, addresses("vnexpress.net", 40)...)
	h.put("hplt3", 0, addresses("tuoitre.vn", 40)...)

	got, report := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1, Limit: 10})
	if len(got) != 10 {
		t.Fatalf("a limit of ten gave %d addresses", len(got))
	}
	if report.Parts != 1 {
		t.Errorf("a limit inside the first part still read %d parts", report.Parts)
	}
}

// One source out of a dataset holding several, which is how a run seeds itself
// from the highest quality upstream rather than from all of them.
func TestOneSourceCanBeReadOnItsOwn(t *testing.T) {
	h := newHub(t)
	h.put("fineweb2", 0, addresses("vnexpress.net", 10)...)
	h.put("hplt3", 0, addresses("tuoitre.vn", 10)...)

	got, report := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1, Prefix: "hplt3"})
	if report.Parts != 1 || len(got) != 10 {
		t.Fatalf("one source came out as %d addresses over %d parts", len(got), report.Parts)
	}
	for _, address := range got {
		if !strings.Contains(address, "tuoitre.vn") {
			t.Errorf("reading hplt3 turned up %s", address)
		}
	}
}

// This is the claim the whole route rests on. If seeding a crawl meant moving
// the corpus back out of the Hub it would not be worth doing at all.
func TestReadingTheAddressesDoesNotMoveTheText(t *testing.T) {
	const docs = 20000
	h := newHub(t)
	h.put("fineweb2", 0, addresses("vnexpress.net", docs)...)

	h.mu.Lock()
	var size int64
	for _, body := range h.files {
		size += int64(len(body))
	}
	h.mu.Unlock()

	if _, report := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1}); report.Kept != docs {
		t.Fatalf("%d addresses came out as %d", docs, report.Kept)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	// Half rather than all of it. The read has to fetch the footer as well as
	// the column, and against a fixture this small the footer is a real share of
	// the file where against a real part it is a rounding error, so a test that
	// demanded a real part's ratio would be a test about the fixture.
	if h.served > size/2 {
		t.Errorf("reading the url column moved %d bytes of a %d byte part, which is most of the file", h.served, size)
	}
	t.Logf("%d bytes moved of %d", h.served, size)
}

func TestAnEmptyRepoSaysSoRatherThanPrintingNothing(t *testing.T) {
	h := newHub(t)
	h.put("fineweb2", 0, addresses("vnexpress.net", 10)...)

	_, err := Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, Prefix: "glotcc"}.
		Read(t.Context(), func(string) error { return nil })
	if err == nil {
		t.Fatal("reading a source that is not there came back without an error")
	}
	if !strings.Contains(err.Error(), "glotcc") {
		t.Errorf("the error does not name what was asked for: %v", err)
	}
}

// A crawler that keeps text/html and decides on the Content-Type pays for a PDF
// in full before it drops it, and a PDF is megabytes where a page is kilobytes.
func TestAddressesThisCrawlerCannotReadAreLeftOut(t *testing.T) {
	h := newHub(t)
	h.put("finepdfs", 0,
		"https://vise.com.vn/tai-lieu.pdf",
		"https://moet.gov.vn/thong-tu-2024.PDF",
		"https://uni.edu.vn/luan-van.docx",
		"https://cdn.vn/anh.jpg",
		"https://vnexpress.net/bai-viet.html",
		"https://tuoitre.vn/khong-co-duoi",
		"https://luatvietnam.vn/tai-file.pdf.aspx",
	)

	got, report := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1})
	if len(got) != 3 || report.Binary != 4 {
		t.Fatalf("seven addresses gave %d kept and %d passed over, want 3 and 4: %v", len(got), report.Binary, got)
	}
	for _, address := range got {
		if strings.Contains(strings.ToLower(address), ".pdf") && !strings.HasSuffix(address, ".aspx") {
			t.Errorf("%s reached the seed", address)
		}
	}

	all, _ := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1, Any: true})
	if len(all) != 7 {
		t.Errorf("-any gave %d addresses of seven", len(all))
	}
}

// Path order puts every part of the first source before the first part of the
// second, so a read cut short by a limit would come back holding one upstream.
// The sources are read in rotation instead.
func TestTheSourcesAreReadInRotationSoATruncatedReadIsAMix(t *testing.T) {
	h := newHub(t)
	for i := range 3 {
		h.put("finepdfs", i, addresses(fmt.Sprintf("pdf%d.vn", i), 4)...)
		h.put("fineweb2", i, addresses(fmt.Sprintf("web%d.vn", i), 4)...)
		h.put("hplt3", i, addresses(fmt.Sprintf("hplt%d.vn", i), 4)...)
	}

	got, _ := read(t, Pages{Repo: "open-index/vitco-clean", API: h.srv.URL, PerHost: -1, Limit: 12})
	sources := map[string]bool{}
	for _, address := range got {
		host, _ := hostOf(address)
		sources[strings.TrimRight(strings.TrimSuffix(host, ".vn"), "0123456789")] = true
	}
	if len(sources) != 3 {
		t.Errorf("the first twelve addresses came from %d sources, want all three: %v", len(sources), sources)
	}
}

func TestOnlyAbsoluteHTTPAddressesAreSeeds(t *testing.T) {
	for _, tc := range []struct {
		raw, host string
		ok        bool
	}{
		{"https://VNExpress.NET/a.html", "vnexpress.net", true},
		{"http://tuoitre.vn:8080/b", "tuoitre.vn", true},
		{"ftp://files.vn/c", "", false},
		{"/relative/path", "", false},
		{"", "", false},
	} {
		host, ok := hostOf(tc.raw)
		if ok != tc.ok || host != tc.host {
			t.Errorf("hostOf(%q) = %q, %v, want %q, %v", tc.raw, host, ok, tc.host, tc.ok)
		}
	}
}
