package lap_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/tamnd/gao/lap"
)

// Enough Vietnamese to build documents out of. What matters is the count: a
// generator drawing freely from a vocabulary this size produces five syllable
// grams that are almost all new, which is what a healthy run looks like from
// the outside.
var vocab = strings.Fields(`
thủ tục hành chính công dân giấy tờ nộp hồ sơ tại ủy ban nhân dân xã phường
quận huyện tỉnh thành phố cơ quan tiếp nhận giải quyết trong thời hạn ngày làm
việc kể từ khi nhận đủ theo quy định của pháp luật hiện hành người yêu cầu phải
xuất trình chứng minh thư căn cước công dân hoặc hộ chiếu còn giá trị sử dụng
trường hợp thiếu thì được hướng dẫn bổ sung một lần duy nhất bằng văn bản nêu rõ
lý do và thời gian trả kết quả cho tổ chức cá nhân có liên quan đến việc đăng ký
khai sinh kết hôn thường trú tạm trú chuyển nhượng quyền sử dụng đất nhà ở tài
sản gắn liền với đất theo mẫu do bộ ban hành kèm biểu phí lệ phí thu nộp ngân
sách nhà nước cấp trên trực tiếp kiểm tra giám sát báo cáo định kỳ hàng quý năm
`)

// text is n syllables drawn freely, which is the generator that has not run out.
func text(r *rand.Rand, n int) string {
	out := make([]string, 0, n)
	for range n {
		out = append(out, vocab[r.Intn(len(vocab))])
	}
	return strings.Join(out, " ")
}

// healthy is a run that clears every line: varied text, no shape that dominates,
// forty prompts, and a filter that threw away a tenth.
func healthy() []lap.Doc {
	r := rand.New(rand.NewSource(1))
	docs := make([]lap.Doc, 0, 400)
	for i := range 400 {
		docs = append(docs, lap.Doc{
			ID:     fmt.Sprintf("synth-%04d", i),
			Prompt: fmt.Sprintf("p%02d", i%40),
			Domain: "administrative",
			Text:   text(r, 120),
			Kept:   i%10 != 0,
		})
	}
	return docs
}

func set(docs []lap.Doc) lap.Set { return lap.Read("gao-synth-1.0", docs) }

func says(t *testing.T, lines []string, want string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, want) {
			return
		}
	}
	t.Errorf("nothing said %q, and what came back was:\n  %s", want, strings.Join(lines, "\n  "))
}

func silent(t *testing.T, lines []string, unwanted string) {
	t.Helper()
	for _, l := range lines {
		if strings.Contains(l, unwanted) {
			t.Errorf("something said %q and should not have:\n  %s", unwanted, l)
		}
	}
}

func TestARunThatKeepsProducingNewMaterialHolds(t *testing.T) {
	s := set(healthy())

	if why := s.Blocking(); len(why) > 0 {
		t.Fatalf("an ordinary run was refused:\n  %s", strings.Join(why, "\n  "))
	}
	if faults := s.Faults(); len(faults) > 0 {
		t.Fatalf("an ordinary run carries faults:\n  %s", strings.Join(faults, "\n  "))
	}
	if !s.Holds() {
		t.Error("a run with nothing wrong with it does not hold")
	}
	if s.Kept != 360 || s.Rejected != 40 {
		t.Errorf("%d kept and %d rejected, want 360 and 40", s.Kept, s.Rejected)
	}
	if s.Novelty < 0.9 {
		t.Errorf("a set drawn freely from the vocabulary came back %.1f%% new, which means the measure is not measuring what it says", s.Novelty*100)
	}
}

// The failure the package exists for. Every document here is fluent, none of
// them repeats itself, no two are near duplicates, and the set is twenty
// sentences with the order changed.
func TestASetThatHasRunOutOfThingsToSayIsCaughtWhereNoDocumentIs(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	sentences := make([]string, 0, 20)
	for range 20 {
		sentences = append(sentences, text(r, 14))
	}

	docs := make([]lap.Doc, 0, 400)
	for i := range 400 {
		var b strings.Builder
		for j := range 4 {
			if j > 0 {
				b.WriteString(". ")
			}
			b.WriteString(sentences[(i*7+j*3)%len(sentences)])
		}
		docs = append(docs, lap.Doc{
			ID:     fmt.Sprintf("synth-%04d", i),
			Prompt: fmt.Sprintf("p%02d", i%40),
			Text:   b.String(),
			Kept:   i%10 != 0,
		})
	}

	s := set(docs)
	if s.Novelty > 0.05 {
		t.Errorf("a set of twenty sentences came back %.1f%% new", s.Novelty*100)
	}
	says(t, s.Faults(), "the last tenth of the set is 0.0% material the rest of it did not already hold")
	says(t, s.Faults(), "producing length rather than content")
	if s.Holds() {
		t.Error("a set that ran out of things to say holds")
	}

	// And it is not a refusal, because the run happened and the numbers are the
	// argument for stopping it rather than for deleting it.
	if why := s.Blocking(); len(why) > 0 {
		t.Errorf("a measurable set was refused:\n  %s", strings.Join(why, "\n  "))
	}
}

// Novelty is measured in the order the run was generated, so a set that is
// perfectly varied for nine tenths and repeats itself at the end is caught, and
// the same documents shuffled would not be. The order is the measurement.
func TestNoveltyIsReadOverTheOrderTheRunWasGeneratedIn(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	stuck := text(r, 200)

	docs := make([]lap.Doc, 0, 400)
	for i := range 400 {
		body := text(r, 120)
		if i >= 360 {
			body = stuck
		}
		docs = append(docs, lap.Doc{
			ID:     fmt.Sprintf("synth-%04d", i),
			Prompt: fmt.Sprintf("p%02d", i%40),
			Text:   body,
			Kept:   true,
		})
	}

	s := set(docs)
	if s.Novelty > 0.05 {
		t.Errorf("a run that got stuck for its last tenth came back %.1f%% new", s.Novelty*100)
	}
	says(t, s.Faults(), "producing length rather than content")
}

// A shape is what a reader of the set notices first and what no per document
// filter can see, since the shape is only visible against the other documents.
func TestOneShapeAcrossTheSetIsNamedAndQuoted(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	opening := text(r, 8)

	docs := make([]lap.Doc, 0, 400)
	for i := range 400 {
		body := text(r, 120)
		if i%5 == 0 {
			body = opening + " " + body
		}
		docs = append(docs, lap.Doc{
			ID:     fmt.Sprintf("synth-%04d", i),
			Prompt: fmt.Sprintf("p%02d", i%40),
			Text:   body,
			Kept:   true,
		})
	}

	s := set(docs)
	if len(s.Shapes) == 0 || s.Shapes[0].Share < 0.19 {
		t.Fatalf("the opening a fifth of the set shares came back as %v", s.Shapes)
	}
	says(t, s.Faults(), "20.0% of the documents open with the same 8 syllables")
	says(t, s.Faults(), "one shape with the nouns changed")
}

// Everything cheap gets run the most, so one prompt carrying the set is the
// ordinary result of a targeting plan rather than an unusual one.
func TestOnePromptCarryingTheSetIsTheSameFailureThroughAnotherDoor(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	docs := make([]lap.Doc, 0, 400)
	for i := range 400 {
		prompt := fmt.Sprintf("p%02d", i%40)
		if i%4 == 0 {
			prompt = "p-cheap"
		}
		docs = append(docs, lap.Doc{
			ID:     fmt.Sprintf("synth-%04d", i),
			Prompt: prompt,
			Text:   text(r, 120),
			Kept:   true,
		})
	}

	s := set(docs)
	if s.Prompts[0].Text != "p-cheap" {
		t.Fatalf("the prompt that carried the set came back as %v", s.Prompts[0])
	}
	says(t, s.Faults(), "25.0% of what shipped came from the prompt p-cheap")
}

// The reject rate is wrong at both ends and for different reasons, which is why
// it is two lines rather than one threshold.
func TestTheRejectRateIsReadAtBothEnds(t *testing.T) {
	t.Run("nothing rejected", func(t *testing.T) {
		docs := healthy()
		for i := range docs {
			docs[i].Kept = true
		}

		s := set(docs)
		says(t, s.Faults(), "0.0% of what was generated was rejected")
		says(t, s.Faults(), "a filter that did not run rather than a generator that needed none")
	})

	t.Run("most of it rejected", func(t *testing.T) {
		docs := healthy()
		for i := range docs {
			docs[i].Kept = i%4 == 0
		}

		s := set(docs)
		says(t, s.Faults(), "75.0% of what was generated was rejected")
		says(t, s.Faults(), "the card has to say that")
	})

	t.Run("a tenth rejected", func(t *testing.T) {
		silent(t, set(healthy()).Faults(), "was rejected")
	})
}

func TestASetThatCannotBeMeasuredIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spoil func(docs []lap.Doc) []lap.Doc
		want  string
	}{
		{"no documents", func([]lap.Doc) []lap.Doc { return nil }, "holds no documents"},
		{"too few documents", func(docs []lap.Doc) []lap.Doc { return docs[:100] }, "under the 200 this measure needs"},
		{"a document with no identity", func(docs []lap.Doc) []lap.Doc { docs[3].ID = ""; return docs }, "no identity"},
		{"a document twice", func(docs []lap.Doc) []lap.Doc { docs[3].ID = docs[2].ID; return docs }, "appears twice"},
		{"a document with no prompt", func(docs []lap.Doc) []lap.Doc { docs[3].Prompt = ""; return docs }, "does not say which prompt made it"},
		{"a kept document with no text", func(docs []lap.Doc) []lap.Doc { docs[3].Text = "  "; return docs }, "was kept and holds no text"},
		{"everything rejected", func(docs []lap.Doc) []lap.Doc {
			for i := range docs {
				docs[i].Kept = false
			}
			return docs
		}, "every document was rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := set(tc.spoil(healthy()))

			why := s.Blocking()
			if len(why) == 0 {
				t.Fatalf("the set was accepted and should have been refused for %q", tc.want)
			}
			says(t, why, tc.want)
			if s.Holds() {
				t.Error("a set that was refused also holds")
			}
			if len(s.Faults()) != 0 {
				t.Errorf("a refused set also reported faults:\n  %s", strings.Join(s.Faults(), "\n  "))
			}
		})
	}
}

// Generated text with no generator on it is the one thing this project will not
// publish, so it is refused before anything is counted.
func TestASetWithNoGeneratorIsRefusedBeforeAnythingElse(t *testing.T) {
	s := lap.Read("", healthy())

	why := s.Blocking()
	if len(why) != 1 {
		t.Fatalf("a set with no generator came back with %d refusals, want 1:\n  %s", len(why), strings.Join(why, "\n  "))
	}
	says(t, why, "does not name the generator that wrote it")
	if !strings.Contains(s.Verdict(), "does not name the generator") {
		t.Errorf("the verdict does not lead with the refusal:\n%s", s.Verdict())
	}
}

func TestTheVerdictCarriesTheNumbersTheDecisionIsMadeOn(t *testing.T) {
	v := set(healthy()).Verdict()

	for _, want := range []string{
		"gao-synth-1.0 kept 360 of 400 documents",
		"the last tenth of what it kept is",
		"grams of five syllables",
		"Nothing in the set says it has run out of things to say",
	} {
		if !strings.Contains(v, want) {
			t.Errorf("the verdict does not say %q:\n%s", want, v)
		}
	}
}
