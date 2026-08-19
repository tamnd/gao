package pick

import (
	"strings"
	"testing"
)

func TestAnAddressIsReadOrRejected(t *testing.T) {
	for _, tt := range []struct {
		in     string
		scheme string
		ask    string
	}{
		{"hf:openai/openai_humaneval", HuggingFace, "https://huggingface.co/api/datasets/openai/openai_humaneval"},
		{"git:https://github.com/ZaloAI-Jaist/VMLU", Git, "https://github.com/ZaloAI-Jaist/VMLU/commits"},
		{"git:https://github.com/ZaloAI-Jaist/VMLU.git", Git, "https://github.com/ZaloAI-Jaist/VMLU/commits"},
		{"gao:needle frame", Built, "gao needle frame"},
		{"gao:hesitate items", Built, "gao hesitate items"},
	} {
		h, err := ParseHome(tt.in)
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if h.Scheme != tt.scheme {
			t.Errorf("%q has scheme %q and should have %q", tt.in, h.Scheme, tt.scheme)
		}
		if h.Ask() != tt.ask {
			t.Errorf("%q is asked at %q and should be asked at %q", tt.in, h.Ask(), tt.ask)
		}
	}

	for _, in := range []string{
		"",
		"openai/openai_humaneval",
		"hf:openai",
		"hf:openai/",
		"hf:/openai_humaneval",
		"hf:openai/humaneval/main",
		"git:github.com/ZaloAI-Jaist/VMLU",
		"http:example.vn/benchmark",
		"gao:",
		"gao:kim  frame",
		"gao: needle frame",
		"gao:needle frame -json",
		"gao:print the frame kim is built on",
		"gao:KIM frame",
	} {
		if h, err := ParseHome(in); err == nil {
			t.Errorf("%q was read as %+v", in, h)
		}
	}
}

// A home is written back out as it was written down, because it goes into the
// roster and a round trip that changes it changes the file.
func TestAnAddressComesBackOutAsItWentIn(t *testing.T) {
	const in = "hf:uitnlp/vietnamese_students_feedback"
	h, err := ParseHome(in)
	if err != nil {
		t.Fatal(err)
	}
	if h.String() != in {
		t.Errorf("%q came back as %q", in, h.String())
	}
}

const sha = "7dce6050a7d6d172f3cc5c32aa97f52fa1a2e544"

// This is the rule the whole file exists for. A release note that names 2.0 has
// named something that can be reuploaded, and a reader who goes to check it a
// year later gets whatever is there now.
func TestAVersionNumberIsNotARevision(t *testing.T) {
	for _, tt := range []struct {
		name    string
		version string
		pinned  bool
	}{
		{"an object id", sha, true},
		{"a version number", "2.0", false},
		{"a tag", "v1.0.0", false},
		{"a branch", "main", false},
		{"the word", Unpinned, false},
		{"nothing", "", false},
		{"half an object id", sha[:20], false},
		{"an object id with a capital in it", strings.ToUpper(sha), false},
		{"forty characters that are not hex", strings.Repeat("z", 40), false},
	} {
		e := Entry{Name: "b", Origin: Native, Version: tt.version, Home: "hf:openai/openai_humaneval"}
		if e.Pinned() != tt.pinned {
			t.Errorf("%s: %q came back pinned %v", tt.name, tt.version, e.Pinned())
		}
	}
}

func TestARevisionWithNoHomeIsNotAPin(t *testing.T) {
	e := Entry{Name: "vmlu", Origin: Native, Version: sha}
	if e.Pinned() {
		t.Error("a revision with nowhere to ask for it came back pinned")
	}
	err := e.checkPin()
	if err == nil {
		t.Fatal("a revision with no home was accepted")
	}
	if !strings.Contains(err.Error(), "nobody can check") {
		t.Errorf("the error is %q and does not say what is wrong with it", err)
	}
}

func TestAnUnpinnedBenchmarkHasToSayWhatItIsWaitingFor(t *testing.T) {
	e := Entry{Name: "vinli", Origin: Native, Version: Unpinned}
	err := e.checkPin()
	if err == nil {
		t.Fatal("an unpinned benchmark with no reason was accepted, and it will be a surprise at release time")
	}
	if !strings.Contains(err.Error(), "vinli") {
		t.Errorf("the error is %q and does not name the benchmark", err)
	}

	e.Pending = "no copy published by its authors"
	if err := e.checkPin(); err != nil {
		t.Errorf("an unpinned benchmark that says why was rejected: %v", err)
	}
}

func TestAPinnedBenchmarkIsNotStillWaitingForSomething(t *testing.T) {
	e := Entry{
		Name:    "humaneval",
		Origin:  Native,
		Version: sha,
		Home:    "hf:openai/openai_humaneval",
		Pending: "waiting for an address",
	}
	if err := e.checkPin(); err == nil {
		t.Error("an entry that is both pinned and waiting was accepted, and one of the two is stale")
	}
}

func TestANameWhereARevisionShouldBeIsRejectedByName(t *testing.T) {
	e := Entry{Name: "vimmrc", Origin: Native, Version: "2.0", Home: "hf:uitnlp/vimmrc2.0"}
	err := e.checkPin()
	if err == nil {
		t.Fatal("a version number was accepted as a revision")
	}
	for _, want := range []string{"vimmrc", "2.0", "object id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error is %q and does not mention %q", err, want)
		}
	}
}

func TestARosterWithABadPinDoesNotLoad(t *testing.T) {
	const bad = `{"version":"1","benchmarks":[
		{"name":"vmlu","version":"2.0","home":"hf:a/b","origin":"native"}]}`
	if _, err := DecodeRoster(strings.NewReader(bad)); err == nil {
		t.Error("a roster naming a version number as a revision loaded")
	}

	const noHome = `{"version":"1","benchmarks":[
		{"name":"vmlu","version":"` + sha + `","origin":"native"}]}`
	if _, err := DecodeRoster(strings.NewReader(noHome)); err == nil {
		t.Error("a roster with a revision and no home loaded")
	}

	const noReason = `{"version":"1","benchmarks":[
		{"name":"vmlu","version":"unpinned","origin":"native"}]}`
	if _, err := DecodeRoster(strings.NewReader(noReason)); err == nil {
		t.Error("a roster with a silent gap in it loaded")
	}
}

// The roster in the repository is the one that matters, and these are the two
// halves of the claim it makes: what is pinned is pinned at something checkable,
// and what is not says why in a sentence somebody can act on.
func TestTheRosterInTheRepositoryPinsWhatItCanAndAccountsForTheRest(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatal(err)
	}
	var pinned, waiting int
	for _, e := range ros.Benchmarks {
		if e.Pinned() {
			pinned++
			if _, err := ParseHome(e.Home); err != nil {
				t.Errorf("%s: %v", e.Name, err)
			}
			continue
		}
		waiting++
		// A reason is a sentence, not a shrug. The length is a floor rather
		// than a judgement of the prose: "todo" passes any check that only
		// asks whether the field is set.
		if len(e.Pending) < 40 {
			t.Errorf("%s is waiting on %q, which is not a reason anybody can act on", e.Name, e.Pending)
		}
		if !strings.HasSuffix(strings.TrimSpace(e.Pending), ".") {
			t.Errorf("%s gives its reason as %q, which is not written as prose", e.Name, e.Pending)
		}
	}
	if pinned == 0 {
		t.Error("nothing on the roster is pinned")
	}
	if pinned+waiting != len(ros.Benchmarks) {
		t.Errorf("%d pinned and %d waiting out of %d benchmarks", pinned, waiting, len(ros.Benchmarks))
	}
	t.Logf("%d benchmarks pinned, %d waiting", pinned, waiting)
}

func TestTheUnpinnedBenchmarksComeBackWithTheirReasons(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatal(err)
	}
	blocking := ros.Blocking()
	if len(blocking) != len(ros.Unpinned()) {
		t.Fatalf("%d benchmarks are unpinned and %d gave a reason", len(ros.Unpinned()), len(blocking))
	}
	for i, b := range blocking {
		name, reason, ok := strings.Cut(b, ": ")
		if !ok || reason == "" {
			t.Errorf("%q is not a name and a reason", b)
			continue
		}
		if name != ros.Unpinned()[i] {
			t.Errorf("the reasons are in a different order from the names: %q against %q", name, ros.Unpinned()[i])
		}
	}
}

// The three rows that name a Vietnamese translation the evaluation harness does
// not have are the finding this work turned up, and they are worth pinning in a
// test so that nobody quietly gives them a revision without producing the items.
func TestTheBenchmarksWithNoVietnameseVersionSayThatIsWhatIsWrong(t *testing.T) {
	ros, err := Rostered()
	if err != nil {
		t.Fatal(err)
	}
	missing := map[string]bool{"gsm8k-vi": true, "math-vi": true, "winogrande-vi": true}
	for _, e := range ros.Benchmarks {
		if !missing[e.Name] {
			continue
		}
		delete(missing, e.Name)
		if e.Pinned() {
			t.Errorf("%s is pinned, and there was no Vietnamese version of it to pin", e.Name)
		}
		if !strings.Contains(e.Pending, "v0.4.12") {
			t.Errorf("%s says %q, which does not name the harness revision it was checked against", e.Name, e.Pending)
		}
	}
	for name := range missing {
		t.Errorf("%s came off the roster, and a row that is hard to fill is not a row to delete", name)
	}
}

const digest = "5da3e0715e97a43431c73ba8ad65ac9493f0934f0d9518d0fc5c03926d34dc2a"

// A set built here is pinned at the digest its own command prints, which is what
// the sets in this repository had instead of a Hub upload the whole time.
//
// The reason they sat unpinned was that pinning was read as publishing. It is
// not. Publishing is where the items can be downloaded from, and pinning is
// whether two people mean the same set by the same name, and the second one has
// been answerable since the frame was hashed.
func TestASetBuiltHereIsPinnedByItsOwnDigest(t *testing.T) {
	e := Entry{Name: "vi-needle", Version: digest, Home: "gao:needle frame", Origin: Native}
	if !e.Pinned() {
		t.Fatal("a set pinned at the digest its command prints is not pinned")
	}
	if err := e.checkPin(); err != nil {
		t.Fatal(err)
	}
}

// The two lengths are not interchangeable and neither substitution is a typo. A
// Hub repository cannot answer for a digest computed here, and a set built here
// has no forty character revision to give, so an entry that mixes them reads as
// pinned to a reader and cannot be checked by one.
func TestARevisionHasToBeTheKindItsHomeAnswersWith(t *testing.T) {
	for _, tt := range []struct {
		name    string
		version string
		home    string
		want    string
	}{
		{"vi-needle", sha, "gao:needle frame", "digest its command prints"},
		{"humaneval", digest, "hf:openai/openai_humaneval", "answers with a 40 character object id"},
	} {
		e := Entry{Name: tt.name, Version: tt.version, Home: tt.home, Origin: Native}
		err := e.checkPin()
		if err == nil {
			t.Errorf("%s at %s said nothing", tt.name, tt.home)
			continue
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}
