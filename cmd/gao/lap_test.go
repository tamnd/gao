package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var lapVocab = strings.Fields(`
thủ tục hành chính công dân giấy tờ nộp hồ sơ tại ủy ban nhân dân xã phường
quận huyện tỉnh thành phố cơ quan tiếp nhận giải quyết trong thời hạn ngày làm
việc kể từ khi nhận đủ theo quy định của pháp luật hiện hành người yêu cầu phải
xuất trình chứng minh thư căn cước công dân hoặc hộ chiếu còn giá trị sử dụng
trường hợp thiếu thì được hướng dẫn bổ sung một lần duy nhất bằng văn bản nêu rõ
lý do và thời gian trả kết quả cho tổ chức cá nhân có liên quan đến việc đăng ký
`)

func lapText(r *rand.Rand, n int) string {
	out := make([]string, 0, n)
	for range n {
		out = append(out, lapVocab[r.Intn(len(lapVocab))])
	}
	return strings.Join(out, " ")
}

// lapRun writes a generated run of 400 documents. Each body comes back from
// body, which is what the test is varying, and a tenth of the run is rejected.
func lapRun(t *testing.T, body func(r *rand.Rand, i int) string) string {
	t.Helper()
	r := rand.New(rand.NewSource(7))

	lines := make([]string, 0, 400)
	for i := range 400 {
		line, err := json.Marshal(map[string]any{
			"id":     fmt.Sprintf("synth-%04d", i),
			"prompt": fmt.Sprintf("p%02d", i%40),
			"domain": "administrative",
			"text":   body(r, i),
			"kept":   i%10 != 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(line))
	}

	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// varied is the generator that has not run out of things to say.
func varied(t *testing.T) string {
	return lapRun(t, func(r *rand.Rand, _ int) string { return lapText(r, 120) })
}

// collapsed is twenty sentences with the order changed, which is the failure no
// per document filter can see.
func collapsed(t *testing.T) string {
	r := rand.New(rand.NewSource(8))
	sentences := make([]string, 0, 20)
	for range 20 {
		sentences = append(sentences, lapText(r, 14))
	}
	return lapRun(t, func(_ *rand.Rand, i int) string {
		out := make([]string, 0, 4)
		for j := range 4 {
			out = append(out, sentences[(i*7+j*3)%len(sentences)])
		}
		return strings.Join(out, ". ")
	})
}

func TestLapPassesARunThatKeepsProducingNewMaterial(t *testing.T) {
	out, errOut, code := exec(t, "lap", "-generator", "gao-synth-1.0", varied(t))

	if code != 0 {
		t.Fatalf("an ordinary run: exit %d, want 0\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"gao-synth-1.0 wrote 400 documents and its own filter kept 360 of them, which is 10.0% rejected",
		"The openings the most documents share, at 8 syllables each:",
		"The prompts the most of what shipped came from:",
		"Nothing in the set says it has run out of things to say",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "shorter than its token count") {
		t.Errorf("an ordinary run reported faults:\n%s", out)
	}
}

func TestLapCatchesASetThatHasRunOutOfThingsToSay(t *testing.T) {
	out, errOut, code := exec(t, "lap", "-generator", "gao-synth-1.0", collapsed(t))

	if code != 2 {
		t.Fatalf("a set of twenty sentences: exit %d, want 2\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"The last tenth of what it kept is 0% material the first nine tenths did not already hold",
		"producing length rather than content",
		"This set is shorter than its token count:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}
}

// Everything cheap gets run the most, so one prompt carrying the set is the
// ordinary result of a targeting plan rather than an unusual one, and the report
// has to name the prompt rather than only report a share.
func TestLapNamesThePromptThatCarriedTheSet(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	lines := make([]string, 0, 400)
	for i := range 400 {
		prompt := fmt.Sprintf("p%02d", i%40)
		if i%4 == 0 {
			prompt = "p-thu-tuc-hanh-chinh"
		}
		line, err := json.Marshal(map[string]any{
			"id":     fmt.Sprintf("synth-%04d", i),
			"prompt": prompt,
			"text":   lapText(r, 120),
			"kept":   i%10 != 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(line))
	}
	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "lap", "-generator", "gao-synth-1.0", path)
	if code != 2 {
		t.Fatalf("a set a quarter of which came from one prompt: exit %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "came from the prompt p-thu-tuc-hanh-chinh") {
		t.Errorf("the report does not name the prompt:\n%s", out)
	}
}

func TestLapPrintsTheSameReadingAsJSON(t *testing.T) {
	out, _, code := exec(t, "lap", "-generator", "gao-synth-1.0", "-json", collapsed(t))
	if code != 2 {
		t.Fatalf("exit %d, want 2\n%s", code, out)
	}

	var got struct {
		Generator  string  `json:"generator"`
		Docs       int     `json:"docs"`
		Kept       int     `json:"kept"`
		Rejected   int     `json:"rejected"`
		RejectRate float64 `json:"reject_rate"`
		Novelty    float64 `json:"novelty"`
		Tail       int     `json:"tail"`
		Prompts    []struct {
			Text  string  `json:"text"`
			Docs  int     `json:"docs"`
			Share float64 `json:"share"`
		} `json:"prompts"`
		Faults []string `json:"faults"`
		Holds  bool     `json:"holds"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the JSON does not parse: %v\n%s", err, out)
	}

	if got.Generator != "gao-synth-1.0" {
		t.Errorf("the generator came back as %q", got.Generator)
	}
	if got.Docs != 400 || got.Kept != 360 || got.Rejected != 40 {
		t.Errorf("%d documents, %d kept, %d rejected, want 400, 360 and 40", got.Docs, got.Kept, got.Rejected)
	}
	if got.RejectRate != 0.1 {
		t.Errorf("the reject rate came back as %v, want 0.1", got.RejectRate)
	}
	if got.Novelty != 0 {
		t.Errorf("a set of twenty sentences came back %v new", got.Novelty)
	}
	if got.Tail == 0 {
		t.Error("the report does not say how much text the novelty figure was read over")
	}
	if len(got.Prompts) == 0 || got.Prompts[0].Docs == 0 {
		t.Errorf("the prompts came back as %v", got.Prompts)
	}
	if len(got.Faults) == 0 || got.Holds {
		t.Errorf("the reading came back with %d faults and holds=%v", len(got.Faults), got.Holds)
	}
}

// Generated text with no generator on it is the one thing this project will not
// publish, so it is refused rather than measured and reported.
func TestLapRefusesASetWithNoGenerator(t *testing.T) {
	out, errOut, code := exec(t, "lap", varied(t))

	if code != 1 {
		t.Fatalf("a set with no generator: exit %d, want 1\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "does not name the generator that wrote it") {
		t.Errorf("the refusal does not say what is missing:\n%s", out)
	}
	if !strings.Contains(out, "This is not a set anybody can measure") {
		t.Errorf("the refusal does not lead with what it refused:\n%s", out)
	}
}

func TestLapRefusesASetTooSmallToReadACurveOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	lines := make([]string, 0, 40)
	r := rand.New(rand.NewSource(10))
	for i := range 40 {
		lines = append(lines, fmt.Sprintf(`{"id":"synth-%04d","prompt":"p01","text":%q,"kept":true}`, i, lapText(r, 120)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, code := exec(t, "lap", "-generator", "gao-synth-1.0", path)
	if code != 1 {
		t.Fatalf("a set of 40 documents: exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "under the 200 this measure needs") {
		t.Errorf("the refusal does not say how many documents it needs:\n%s", out)
	}
}

func TestLapSaysWhichLineOfTheRunIsWrong(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.jsonl")
	if err := os.WriteFile(path, []byte(`{"id":"synth-0001","prompt":"p01","text":"hồ sơ","kept":true}
{"id":"synth-0002","prompt":"p01","text":"hồ sơ","kept":true,"score":0.9}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := exec(t, "lap", "-generator", "gao-synth-1.0", path)
	if code != 1 {
		t.Fatalf("a run file with a column nobody reads: exit %d, want 1", code)
	}
	if !strings.Contains(errOut, ":2:") || !strings.Contains(errOut, "score") {
		t.Errorf("the failure does not name the line and the column:\n%s", errOut)
	}
}

func TestLapUsageErrors(t *testing.T) {
	if _, _, code := exec(t, "lap"); code != 2 {
		t.Errorf("no file: exit %d, want 2", code)
	}

	_, errOut, code := exec(t, "lap", "-h")
	if code != 2 {
		t.Errorf("gao repeat -h: exit %d, want 2", code)
	}
	for _, want := range []string{
		"one prompt run a million times",
		"reject rate is read at both ends",
		"the last tenth of the set",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the usage does not explain %q:\n%s", want, errOut)
		}
	}
}

func TestLapIsInTheCommandList(t *testing.T) {
	out, _, code := exec(t, "help")
	if code != 0 {
		t.Fatalf("gao help: exit %d", code)
	}
	if !strings.Contains(out, "one prompt run a million times") {
		t.Errorf("lap is not in the command list:\n%s", out)
	}
}
