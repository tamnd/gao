package normalize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkMarkup runs the whole reshape pass over the golden corpus, which is
// the pass a crawl spends most of its CPU in.
//
// It is here because of a profile rather than a hunch. On a live crawl of
// server2, normalize.(*Result).reshape was 30.2% of the crawler's whole CPU and
// the syllable walk under it was 28.7%, against 15.6% for writing the WARC and
// 7.8% for parsing the HTML. A crawler that is short of pages a second is short
// of CPU here first.
//
// GAO_BENCH_TEXT points this at a file instead, which is how it gets run over
// something the size of a real page rather than the corpus.
func BenchmarkMarkup(b *testing.B) {
	text := benchText(b)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		Markup(text)
	}
}

func benchText(b *testing.B) string {
	b.Helper()
	if p := os.Getenv("GAO_BENCH_TEXT"); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			b.Fatalf("GAO_BENCH_TEXT: %v", err)
		}
		return string(raw)
	}
	ins, err := filepath.Glob("testdata/*.in")
	if err != nil {
		b.Fatal(err)
	}
	if len(ins) == 0 {
		b.Fatal("no golden documents in testdata")
	}
	var all strings.Builder
	for _, in := range ins {
		raw, err := os.ReadFile(in)
		if err != nil {
			b.Fatal(err)
		}
		all.Write(raw)
		all.WriteString("\n")
	}
	return all.String()
}
