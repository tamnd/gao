package follow

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	inViet = "Câu hỏi này có câu trả lời khá rõ ràng. Phần dưới đây trình bày từng ý một, theo thứ tự dễ làm trước. Nếu bạn cần thêm chi tiết thì cứ hỏi tiếp."
	inEng  = "Here is the answer you asked for. It is written in English because that is what the question asked for. Let me know if you would like it shorter."
	inBare = "Cau hoi nay co cau tra loi kha ro rang. Phan duoi day trinh bay tung y mot, theo thu tu de lam truoc. Neu ban can them chi tiet thi cu hoi tiep."
)

// working builds what a model that stays in the language would return.
func working(t *testing.T) (Set, []Reply) {
	t.Helper()
	s := Fixed()
	digest := s.Digest()
	replies := make([]Reply, 0, len(s.Items))
	for _, it := range s.Items {
		text := inViet
		if it.Wants == Eng {
			text = inEng
		}
		replies = append(replies, Reply{ID: it.ID, Set: digest, Text: text})
	}
	return s, replies
}

func TestTheFixedSetIsASet(t *testing.T) {
	s := Fixed()
	if faults := s.Faults(); len(faults) != 0 {
		t.Fatalf("the set we publish was faulted:\n%s", strings.Join(faults, "\n"))
	}
	if n := s.Wanting(Eng); n < English {
		t.Errorf("%d items ask for English back, and %d is the floor", n, English)
	}
	if n := len(s.Items); n < Items {
		t.Errorf("the set holds %d items", n)
	}
}

func TestASetThatOnlyEverWantsVietnameseIsRefused(t *testing.T) {
	s := Fixed()
	for i := range s.Items {
		s.Items[i].Wants = Viet
	}
	faults := strings.Join(s.Faults(), "\n")
	if !strings.Contains(faults, "learned never to write anything else") {
		t.Errorf("a set with no English items was allowed:\n%s", faults)
	}
}

func TestAPromptAskedWithoutToneMarksIsRefused(t *testing.T) {
	s := Fixed()
	s.Items[0].Prompt = "Nau pho bo tai nha thi nuoc dung can nhung gi?"
	faults := strings.Join(s.Faults(), "\n")
	if !strings.Contains(faults, "the premise of every item here is a question written in Vietnamese") {
		t.Errorf("a prompt with the marks stripped was allowed:\n%s", faults)
	}
}

func TestTheDigestMovesWithWhatIsAskedAndNotWithWhy(t *testing.T) {
	base := Fixed().Digest()

	same := Fixed()
	same.Items[0].Why = "rewritten to say the same thing better"
	same.Note = "a longer note"
	if same.Digest() != base {
		t.Error("improving the prose around an item changed the identity of the set")
	}

	for _, tc := range []struct {
		name   string
		change func(*Set)
	}{
		{"the prompt", func(s *Set) { s.Items[0].Prompt += " Vì sao?" }},
		{"the language it wants back", func(s *Set) { s.Items[0].Wants = Eng }},
		{"what kind of item it is", func(s *Set) { s.Items[0].Kind = "long" }},
		{"the version", func(s *Set) { s.Version = "1.1" }},
		{"an item leaving", func(s *Set) { s.Items = s.Items[:len(s.Items)-1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Fixed()
			tc.change(&s)
			if s.Digest() == base {
				t.Errorf("changing %s left the digest where it was", tc.name)
			}
		})
	}
}

func TestASentenceIsPlacedInItsLanguage(t *testing.T) {
	for _, tc := range []struct {
		text string
		want Lang
	}{
		{"Phần này giải thích vì sao lãi suất tăng thì giá trái phiếu giảm", Viet},
		{"This section explains why bond prices fall when rates rise", Eng},
		{"Phan nay giai thich vi sao lai suat tang thi gia trai phieu giam", Bare},
		{"HTTPS", Other},
		{"2026", Other},
	} {
		if got := Classify(tc.text); got != tc.want {
			t.Errorf("%q was placed in %s, want %s", tc.text, got, tc.want)
		}
	}
}

func TestAVietnameseSentenceWithEnglishTermsInItIsVietnamese(t *testing.T) {
	for _, text := range []string{
		"Trong Git thì rebase viết lại lịch sử còn merge thì giữ nguyên, nên rebase nhánh đã đẩy lên remote là điều nên tránh.",
		"Docker container dùng chung nhân của máy chủ, còn máy ảo thì chạy nguyên một hệ điều hành riêng.",
		"Overfitting là khi mô hình học thuộc dữ liệu huấn luyện, nên nó làm tốt trên training set mà kém trên test set.",
	} {
		if got := Classify(text); got != Viet {
			t.Errorf("a Vietnamese sentence carrying English terms was placed in %s: %q", got, text)
		}
	}
}

func TestAnEnglishQuotationDoesNotMakeTheAnswerEnglish(t *testing.T) {
	answer := "Câu lỗi 'connection refused' nghĩa là máy chủ đã từ chối kết nối. Nguyên nhân thường gặp là dịch vụ chưa chạy, sai cổng, hoặc tường lửa chặn. Bạn thử kiểm tra cổng trước rồi mới đến tường lửa."
	if got := Read(answer); got.Wrote != Viet || got.Drifted {
		t.Errorf("a Vietnamese answer quoting English read as %+v", got)
	}
}

func TestACodeBlockIsNeitherLanguage(t *testing.T) {
	answer := "Bạn chạy lệnh sau để xem cổng nào đang mở.\n\n```\nss -ltnp | grep 8080\n```\n\nNếu không thấy dòng nào thì dịch vụ chưa chạy, và đó là nguyên nhân phổ biến nhất."
	got := Read(answer)
	if got.Wrote != Viet || got.Drifted {
		t.Errorf("an answer with a shell command in it read as %+v", got)
	}
}

func TestDriftIsFoundAndSoIsWhereItStarted(t *testing.T) {
	answer := inViet + " " + inEng
	got := Read(answer)
	if !got.Drifted {
		t.Fatalf("an answer that finished in English did not read as drift: %+v", got)
	}
	if got.Opened != Viet {
		t.Errorf("the answer opened in %s", got.Opened)
	}
	if got.At <= 0 || got.At >= 1 {
		t.Errorf("the turn was placed at %v, which is not inside the answer", got.At)
	}
}

func TestAnAnswerThatOpensRightAndEndsWrongIsNotAdherent(t *testing.T) {
	s, replies := working(t)
	for i, r := range replies {
		if r.ID == "long-2" {
			replies[i].Text = inViet + " " + inEng
		}
	}
	sc := s.Grade(replies)
	if sc.Drifted != 1 {
		t.Fatalf("%d answers read as drift", sc.Drifted)
	}
	if sc.Adherent != len(s.Items)-1 {
		t.Errorf("%d answers scored adherent out of %d", sc.Adherent, len(s.Items))
	}
	if sc.DriftedAt <= 0 {
		t.Errorf("the drift was reported at %v", sc.DriftedAt)
	}
	if sc.Passed {
		t.Error("a model that finishes in English passed")
	}
}

func TestAModelThatStaysInTheLanguagePasses(t *testing.T) {
	s, replies := working(t)
	sc := s.Grade(replies)
	if !sc.Passed {
		t.Errorf("a model that stayed in the language did not pass: %+v", sc)
	}
	if sc.Adherence != 1 || sc.Drifted != 0 || sc.InOther != 0 || sc.Bare != 0 {
		t.Errorf("a clean run scored %+v", sc)
	}
	if sc.Detected != len(s.Items) {
		t.Errorf("%d verdicts came from the classifier out of %d items", sc.Detected, len(s.Items))
	}
	if faults := sc.Publishable(); len(faults) != 0 {
		t.Errorf("a clean run was faulted: %v", faults)
	}
}

func TestAModelThatAnswersEverythingInVietnameseFailsTheEnglishItems(t *testing.T) {
	s, replies := working(t)
	for i := range replies {
		replies[i].Text = inViet
	}
	sc := s.Grade(replies)
	if sc.InOther != s.Wanting(Eng) {
		t.Errorf("%d answers were in the wrong language, want %d", sc.InOther, s.Wanting(Eng))
	}
	if sc.Passed {
		t.Error("a model that never writes English passed a set that asks for it")
	}
}

func TestVietnameseWithoutToneMarksIsCountedApart(t *testing.T) {
	s, replies := working(t)
	for i, r := range replies {
		if r.ID == "plain-1" || r.ID == "plain-2" {
			replies[i].Text = inBare
		}
	}
	sc := s.Grade(replies)
	if sc.Bare != 2 {
		t.Fatalf("%d answers read as Vietnamese with the marks off", sc.Bare)
	}
	if sc.InOther != 0 {
		t.Errorf("%d unmarked answers were counted as another language", sc.InOther)
	}
	if math.Abs(sc.BareRate-2.0/float64(len(s.Items))) > 1e-9 {
		t.Errorf("the bare rate came out at %v", sc.BareRate)
	}
	if sc.Passed {
		t.Error("a model writing Vietnamese without tone marks passed")
	}
}

func TestAPersonsCallBeatsTheClassifier(t *testing.T) {
	s, replies := working(t)
	for i, r := range replies {
		if r.ID == "tech-1" {
			replies[i].Text = inEng
			replies[i].Wrote = Viet
		}
	}
	sc := s.Grade(replies)
	if sc.Adherent != len(s.Items) {
		t.Errorf("a human verdict did not override the classifier: %+v", sc)
	}
	if sc.Detected != len(s.Items)-1 {
		t.Errorf("%d verdicts came from the classifier, want %d", sc.Detected, len(s.Items)-1)
	}
}

func TestTheBreakdownIsPerShapeOfPrompt(t *testing.T) {
	s, replies := working(t)
	for i, r := range replies {
		if strings.HasPrefix(r.ID, "long-") {
			replies[i].Text = inViet + " " + inEng
		}
	}
	sc := s.Grade(replies)
	for _, k := range sc.ByKind {
		if k.Kind == "long" {
			if k.Drifted != k.Items {
				t.Errorf("%d of %d long answers read as drift", k.Drifted, k.Items)
			}
			continue
		}
		if k.Drifted != 0 {
			t.Errorf("%s reported %d drifts", k.Kind, k.Drifted)
		}
	}
}

func TestAnItemThatNeverCameBackIsNotSilentlyDropped(t *testing.T) {
	s, replies := working(t)
	sc := s.Grade(replies[:len(replies)-2])
	if len(sc.Missing) != 2 || sc.Passed {
		t.Fatalf("a score over the items that returned passed: %+v", sc)
	}
	if !strings.Contains(strings.Join(sc.Publishable(), "\n"), "whatever the harness did not drop") {
		t.Error("the score does not say it has holes in it")
	}
}

func TestAnAnswerWithNoLanguageInItIsAHarnessFault(t *testing.T) {
	s, replies := working(t)
	for i, r := range replies {
		if r.ID == "quote-1" {
			replies[i].Text = "```\nss -ltnp\n```"
		}
	}
	sc := s.Grade(replies)
	if len(sc.Empty) != 1 || sc.Empty[0] != "quote-1" {
		t.Fatalf("an answer with nothing readable in it scored: %+v", sc.Empty)
	}
	if !strings.Contains(strings.Join(sc.Publishable(), "\n"), "harness fault rather than a score") {
		t.Error("the score does not say the answer could not be read")
	}
}

func TestRepliesToAnotherVersionOfTheSetAreCaught(t *testing.T) {
	s, replies := working(t)
	other := Fixed()
	other.Items[0].Prompt += " Nói rõ hơn giúp tôi."
	replies[0].Set = other.Digest()
	sc := s.Grade(replies)
	if len(sc.Elsewhere) != 1 {
		t.Fatalf("replies against another wording were scored: %+v", sc.Elsewhere)
	}
}

func TestAReplyToAnItemTheSetDoesNotHoldIsCaught(t *testing.T) {
	s, replies := working(t)
	replies = append(replies, Reply{ID: "plain-9", Text: inViet})
	sc := s.Grade(replies)
	if len(sc.Strays) != 1 || sc.Strays[0] != "plain-9" {
		t.Fatalf("a reply to an item nobody wrote was scored: %+v", sc.Strays)
	}
}

func TestASetIsReadBackFromJSON(t *testing.T) {
	s := Fixed()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "set.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest() != s.Digest() {
		t.Error("the set did not survive a round trip through JSON")
	}
}

func TestRepliesAreReadOnePerLine(t *testing.T) {
	_, replies := working(t)
	lines := make([]string, 0, len(replies))
	for _, r := range replies {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	path := filepath.Join(t.TempDir(), "replies.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadReplies(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(replies) {
		t.Errorf("read %d replies out of %d", len(got), len(replies))
	}
}

func TestAnEmptyRepliesFileSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replies.jsonl")
	if err := os.WriteFile(path, []byte("\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReplies(path); err == nil {
		t.Error("a file with no replies in it read as a run")
	}
}

func TestTheSetDescribesItself(t *testing.T) {
	got := Fixed().Describe()
	for _, want := range []string{"asked in Vietnamese", "the whole answer is read"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("the description does not say %q: %s", want, got)
		}
	}
}
