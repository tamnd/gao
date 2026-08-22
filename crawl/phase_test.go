package crawl

// What a page costs, one phase at a time.
//
// The crawler's ceiling is CPU rather than the network, which a profile settled
// and three rounds of seed experiments failed to argue with. That turns the
// throughput target into arithmetic: 250 pages a second on C cores is C/250
// seconds of CPU per page and nothing else, so 32ms on `server3`'s eight cores
// and 24ms on `server2`'s six. A profile says which function is large. It does
// not say how many milliseconds a page costs, and that is the number the target
// is written in.
//
// So these run one phase at a time over real fetched pages and report ms/page
// directly. Point GAO_BENCH_WARC at a volume off a live crawl:
//
//	GAO_BENCH_WARC=/tmp/crawl/warc/gao-00007.warc.gz go test ./crawl -run x -bench Phase -benchtime 3x
//
// There is no built in corpus and no skip onto a made up page. A synthetic page
// gives a number that looks like a measurement and is a measurement of the
// fixture, and the phases here are exactly the ones whose cost depends on what
// real Vietnamese HTML looks like: how much markup surrounds the content, how
// many of its letters carry marks, how long the body runs.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/harvest"
	"github.com/tamnd/gao/normalize"
	"github.com/tamnd/gao/sift"
)

// fetched is one page as it came off the wire, which is what every phase below
// starts from.
type fetched struct {
	visit *harvest.Visit
	base  *url.URL
	page  *Page

	// The two markdown renderings, taken once at load. The normalization
	// benchmarks want the strings and not the render that produced them, and a
	// page renders once, so the corpus holds the result rather than the page
	// holding its parse tree for the length of the run.
	markdown, body string
}

// pages reads the WARC named by GAO_BENCH_WARC.
//
// The default of 500 is a compromise between a corpus wide enough that one
// enormous forum thread does not set the mean and a benchmark that finishes
// while somebody is watching it. GAO_BENCH_PAGES overrides it.
func pages(b *testing.B) []fetched {
	b.Helper()
	// Read once for the whole run. Nine benchmarks times a -count of three is
	// twenty seven reads of the same volume, and reading it is itself a parse of
	// every page, so the cache is most of the wall clock rather than a tidiness.
	loadCorpus.Do(func() { corpus, corpusErr = load() })
	if corpusErr != nil {
		if errors.Is(corpusErr, errNoWARC) {
			b.Skip(corpusErr.Error())
		}
		b.Fatal(corpusErr)
	}
	b.Logf("%s", describe(corpus))
	return corpus
}

var (
	loadCorpus sync.Once
	corpus     []fetched
	corpusErr  error
)

var errNoWARC = errors.New("set GAO_BENCH_WARC to a WARC volume off a real crawl")

func describe(corpus []fetched) string {
	var body, text int
	for _, p := range corpus {
		body += len(p.visit.Body)
		text += len(p.page.Text)
	}
	return fmt.Sprintf("%d pages, %d KB of HTML, %d KB of extracted text, %d KB of HTML a page",
		len(corpus), body/1024, text/1024, body/1024/len(corpus))
}

func load() ([]fetched, error) {
	name := os.Getenv("GAO_BENCH_WARC")
	if name == "" {
		return nil, errNoWARC
	}
	limit := 500
	if v := os.Getenv("GAO_BENCH_PAGES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("GAO_BENCH_PAGES=%q: %w", v, err)
		}
		limit = n
	}

	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	wr, err := harvest.NewWARCReader(f)
	if err != nil {
		return nil, err
	}

	var out []fetched
	for len(out) < limit {
		rec, err := wr.Next()
		if errors.Is(err, harvest.ErrDone) || errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if rec.Type() != "response" {
			continue
		}
		got, err := decode(rec)
		if err != nil {
			// A record the reader cannot make an HTTP response out of is one
			// page of several hundred and not a reason to stop.
			continue
		}
		out = append(out, got)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no pages in %s", name)
	}
	return out, nil
}

// decode turns one response record into a page, which is the parse the Read
// benchmark then repeats.
func decode(rec *harvest.Record) (fetched, error) {
	resp, err := rec.Response()
	if err != nil {
		return fetched{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fetched{}, err
	}
	raw := rec.URI()
	base, err := url.Parse(raw)
	if err != nil {
		return fetched{}, err
	}
	page, err := Read(base, bytes.NewReader(body))
	if err != nil {
		return fetched{}, err
	}
	v := &harvest.Visit{
		URL:    raw,
		Host:   base.Hostname(),
		Status: resp.StatusCode,
		Header: resp.Header,
		Body:   body,
		At:     time.Now(),
	}
	md, whole := page.Render()
	return fetched{visit: v, base: base, page: page, markdown: md, body: whole}, nil
}

// each runs fn over every page once per iteration and reports what one page
// cost, which is the unit the 250 a second target is written in. ns/op over a
// corpus of 500 is not a number anybody can compare to a budget.
func each(b *testing.B, corpus []fetched, fn func(fetched)) {
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, p := range corpus {
			fn(p)
		}
	}
	b.StopTimer()
	ms := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(len(corpus)) / 1e6
	b.ReportMetric(ms, "ms/page")
	b.ReportMetric(1000/ms, "pages/s/core")
}

// eachFresh is [each] with a page parsed afresh for every call and the parse
// left out of the measurement.
//
// It exists because a page renders once. A benchmark that handed the same page
// to Build five hundred times would measure one render and four hundred and
// ninety nine reads of a cached string, which is a fine number and not the one
// the crawler pays.
func eachFresh(b *testing.B, corpus []fetched, fn func(fetched, *Page)) {
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, p := range corpus {
			b.StopTimer()
			page, err := Read(p.base, bytes.NewReader(p.visit.Body))
			if err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			fn(p, page)
		}
	}
	b.StopTimer()
	ms := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(len(corpus)) / 1e6
	b.ReportMetric(ms, "ms/page")
	b.ReportMetric(1000/ms, "pages/s/core")
}

// BenchmarkPhaseRead is the HTML parse and the extraction: html.Parse, the head
// walk, the link walk, the container pick and the text render of it. The two
// markdown renders are no longer in here, which is what [BenchmarkPhaseRender]
// measures and what a rejected page no longer pays.
func BenchmarkPhaseRead(b *testing.B) {
	corpus := pages(b)
	each(b, corpus, func(p fetched) {
		if _, err := Read(p.base, bytes.NewReader(p.visit.Body)); err != nil {
			b.Fatal(err)
		}
	})
}

// BenchmarkPhaseNormalizeText is normalize.Normalize over the extracted text,
// which is the encoding repair and the tone reordering.
func BenchmarkPhaseNormalizeText(b *testing.B) {
	corpus := pages(b)
	each(b, corpus, func(p fetched) {
		normalize.Normalize(p.page.Text)
	})
}

// BenchmarkPhaseRender is the two markdown renderings, the article off the
// container the text came from and the whole document. Only a page that passes
// the sift pays for these, so the average page pays the keep rate times this.
func BenchmarkPhaseRender(b *testing.B) {
	corpus := pages(b)
	eachFresh(b, corpus, func(_ fetched, page *Page) {
		page.Render()
	})
}

// BenchmarkPhaseMarkupMarkdown is the normalization pass over the markdown
// rendering of the content, which carries the headings and the tables.
func BenchmarkPhaseMarkupMarkdown(b *testing.B) {
	corpus := pages(b)
	each(b, corpus, func(p fetched) {
		normalize.Markup(p.markdown)
	})
}

// BenchmarkPhaseMarkupBody is the same pass over the whole document, which is
// the largest of the three strings and the one nobody looks at twice.
func BenchmarkPhaseMarkupBody(b *testing.B) {
	corpus := pages(b)
	each(b, corpus, func(p fetched) {
		normalize.Markup(p.body)
	})
}

// BenchmarkPhaseCount is doc.Chars, doc.Syllables and doc.SumString, the three
// walks over the text that fill the counting columns and the document identity.
func BenchmarkPhaseCount(b *testing.B) {
	corpus := pages(b)
	each(b, corpus, func(p fetched) {
		t := p.page.Text
		sinkChars = doc.Chars(t)
		sinkSyllables = doc.Syllables(t)
		sinkHash = doc.SumString(t)
	})
}

// The counting phase returns three small values and does nothing else, so
// without somewhere to put them the compiler is free to delete the calls and
// the benchmark measures an empty loop.
var (
	sinkChars     uint32
	sinkSyllables uint32
	sinkHash      doc.Hash
)

// BenchmarkPhaseMeasure is sift.Measure, which is what decides whether the page
// is Vietnamese and whether it is writing.
func BenchmarkPhaseMeasure(b *testing.B) {
	corpus := pages(b)
	each(b, corpus, func(p fetched) {
		sift.Measure(p.page.Text)
	})
}

// BenchmarkPhaseBuild is every phase above except the parse, in the order the
// crawler runs them, so the sum of the parts can be checked against the whole.
//
// It is the one number here that carries the keep rate in it, because a page
// the sift refuses leaves Build before the render and the corpus is whatever a
// real crawl fetched.
func BenchmarkPhaseBuild(b *testing.B) {
	corpus := pages(b)
	eachFresh(b, corpus, func(p fetched, page *Page) {
		Build(p.visit, page, BuildOptions{Locator: p.visit.URL})
	})
}

// BenchmarkPhasePage is the parse and the build together, which is what one
// fetched page costs the crawler from bytes to a row.
func BenchmarkPhasePage(b *testing.B) {
	corpus := pages(b)
	each(b, corpus, func(p fetched) {
		page, err := Read(p.base, bytes.NewReader(p.visit.Body))
		if err != nil {
			b.Fatal(err)
		}
		Build(p.visit, page, BuildOptions{Locator: p.visit.URL})
	})
}
