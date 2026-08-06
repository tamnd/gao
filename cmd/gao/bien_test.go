package main

import (
	"strings"
	"testing"
)

// The frontier is the part of a crawl that decides how big the crawl is, and
// none of it shows up in a fetched page. These subcommands exist so that the
// decisions can be read before a crawl rather than inferred from what a crawl
// turned out to have done.

func bienRun(t *testing.T, in string, args ...string) (string, string, int) {
	t.Helper()
	if in != "" {
		old := stdin
		stdin = strings.NewReader(in)
		t.Cleanup(func() { stdin = old })
	}
	var out, errb strings.Builder
	code := run(&out, &errb, append([]string{"bien"}, args...))
	return out.String(), errb.String(), code
}

func TestBienCanonShowsWhatMergedWithWhat(t *testing.T) {
	out, _, code := bienRun(t, "",
		"canon",
		"https://VnExpress.net/tin-tuc",
		"https://vnexpress.net/tin-tuc?utm_source=zalo",
		"https://vnexpress.net/the-thao",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "merge") {
		t.Errorf("two spellings of one page did not report a merge:\n%s", out)
	}
	if !strings.Contains(out, "2 pages out, 1 merged") {
		t.Errorf("the count does not say what happened:\n%s", out)
	}
}

func TestBienCanonSaysWhatItWillNotFollow(t *testing.T) {
	out, _, code := bienRun(t, "", "canon", "javascript:void(0)", "https://vnexpress.net/a")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "skip") {
		t.Errorf("a URL that is not followed was not marked:\n%s", out)
	}
	if !strings.Contains(out, "1 not followed") {
		t.Errorf("the count does not report it:\n%s", out)
	}
}

func TestBienShapeCountsTemplatesRatherThanURLs(t *testing.T) {
	out, _, code := bienRun(t, "", "shape", "-count",
		"https://diendan.vn/thread/1",
		"https://diendan.vn/thread/2",
		"https://diendan.vn/thread/3",
		"https://diendan.vn/lich/2024-03-15",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "3  diendan.vn/thread/<n>") {
		t.Errorf("three URLs off one template were not counted together:\n%s", out)
	}
	if !strings.Contains(out, "2 templates") {
		t.Errorf("the count is wrong:\n%s", out)
	}
}

// The description is the whole reason this prints a shape rather than a hash. A
// person looking at a frontier wants to know what is wrong with a template, not
// what it is called.
func TestBienShapeSaysWhatIsWrongWithATemplate(t *testing.T) {
	out, _, code := bienRun(t, "", "shape",
		"https://lich.vn/su-kien/2024-03-15",
		"https://truong.edu.vn/tin/bai/bai/bai/",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "carries a date") {
		t.Errorf("a dated template was not called out:\n%s", out)
	}
	if !strings.Contains(out, "repeats one segment") {
		t.Errorf("a looping template was not called out:\n%s", out)
	}
}

func TestBienBudgetSaysWhatItWouldNotAskFor(t *testing.T) {
	out, _, code := bienRun(t, "", "budget",
		"https://vnexpress.net/tin-tuc/1",
		"https://truong.edu.vn/tin/bai/bai/bai/",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "ask") || !strings.Contains(out, "skip") {
		t.Errorf("the two outcomes are not both reported:\n%s", out)
	}
	if !strings.Contains(out, "resolving against itself") {
		t.Errorf("the skip does not say why:\n%s", out)
	}
	if !strings.Contains(out, "1 asked for, 1 skipped") {
		t.Errorf("the count is wrong:\n%s", out)
	}
}

func TestBienBudgetReportsWhatEachTemplateSpent(t *testing.T) {
	out, _, code := bienRun(t, "", "budget", "-shapes",
		"https://diendan.vn/thread/1",
		"https://diendan.vn/thread/2",
	)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "diendan.vn/thread/<n>") {
		t.Errorf("the template does not appear in the report:\n%s", out)
	}
	if !strings.Contains(out, "spent") {
		t.Errorf("the report does not say what was spent:\n%s", out)
	}
}

// A frontier is a file, and a file is what gets piped.
func TestBienReadsAFrontierFromStandardInput(t *testing.T) {
	in := `
# a comment, because a hand written seed list has comments in it
https://vnexpress.net/tin-tuc/1
https://vnexpress.net/tin-tuc/2

https://tuoitre.vn/a
`
	out, _, code := bienRun(t, in, "shape", "-count")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "3 urls off 2 templates") {
		t.Errorf("blank lines or comments were counted as URLs:\n%s", out)
	}
}

func TestBienNeedsSomethingToWorkOn(t *testing.T) {
	out, errb, code := bienRun(t, "", "canon")
	if code == 0 {
		t.Errorf("an empty frontier was reported as a success:\n%s", out)
	}
	if !strings.Contains(errb, "no urls") {
		t.Errorf("the error does not say what is missing: %q", errb)
	}
}

func TestBienIsInTheSubcommandList(t *testing.T) {
	var out, errb strings.Builder
	if code := run(&out, &errb, []string{"help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "bien") {
		t.Errorf("bien is not listed:\n%s", out.String())
	}

	out.Reset()
	if code := run(&out, &errb, []string{"bien", "help"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, sub := range []string{"canon", "shape", "budget"} {
		if !strings.Contains(out.String(), sub) {
			t.Errorf("%s is not in the bien usage:\n%s", sub, out.String())
		}
	}
}

func TestBienRefusesASubcommandItDoesNotHave(t *testing.T) {
	var out, errb strings.Builder
	if code := run(&out, &errb, []string{"bien", "frontier"}); code == 0 {
		t.Error("an unknown subcommand exited zero")
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Errorf("the error does not say what happened: %q", errb.String())
	}
}
