package sach

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tamnd/gao/che"
	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/vo"
)

// article is a page of the shape this corpus is mostly made of: a short news
// item with a byline. It is real Vietnamese rather than filler, because every
// threshold the line applies is a count over Vietnamese and filler passes or
// fails them for reasons that have nothing to do with the code under test.
const article = `Hà Nội bước vào đợt nắng nóng đầu tiên của mùa hè năm nay, sớm hơn trung bình nhiều năm khoảng hai tuần.

Nhiệt độ ngoài trời có lúc lên tới 39 độ C, và ngành điện cho biết lượng tiêu thụ trong ngày đã vượt mức của cùng kỳ năm ngoái. Người dân được khuyến cáo hạn chế ra đường trong khoảng từ 11 giờ trưa đến 15 giờ chiều, uống đủ nước, và chú ý tới người già cùng trẻ nhỏ trong gia đình.

Bản tin do đài truyền hình phát lúc 19 giờ nói đợt nóng này còn kéo dài đến cuối tuần, sau đó một đợt không khí lạnh yếu sẽ tràn về và nhiệt độ giảm dần.`

// raw is a document as the ingest wrote it: the contract satisfied, the pipeline
// version still on the ingest's leading zero, and no cleaning stage having
// touched it.
func raw(text string) *doc.Document {
	d := &doc.Document{
		RawID:         doc.SumString("<html>" + text + "</html>"),
		Text:          text,
		SchemaVersion: doc.SchemaVersion,
		Provenance: doc.Provenance{
			Source:          doc.SourceGlotCC,
			SourceLocator:   "hf://datasets/cis-lmu/GlotCC-V1@main/v1.0/vie-Latn/vie-Latn_0.jsonl.zst#42",
			URL:             "https://vnexpress.net/thoi-su/ha-noi-nang-nong-4712345.html",
			Host:            "vnexpress.net",
			FetchedAt:       time.Date(2026, 7, 2, 9, 15, 0, 0, time.UTC),
			MediaType:       "text/html",
			Extractor:       "glotcc@1.0",
			PipelineVersion: "0.1.0",
		},
		Language: doc.Language{
			Lang:       "vie",
			LangScore:  0.99,
			Diacritics: "present",
		},
		Licensing: doc.Licensing{
			LicenseClass:    doc.LicenseOpen,
			LicenseEvidence: "CC0-1.0 on the upstream dataset card",
		},
	}
	d.DocID = doc.SumString(d.Text)
	d.NChars = uint32(utf8.RuneCountInString(d.Text))
	return d
}

func TestLineKeepsAnArticleAndStampsIt(t *testing.T) {
	l := New(1024)
	d := raw(article)
	before := d.DocID

	v := l.Run(d)
	if !v.Kept {
		t.Fatalf("the article was removed at %s for %s", v.Stage, v.Reason)
	}
	if err := d.Admit(); err != nil {
		t.Fatalf("a kept document fails the contract: %v", err)
	}
	if d.PipelineVersion != PipelineVersion {
		t.Errorf("pipeline_version is %q, want %q, since the leading zero means no cleaning stage ran", d.PipelineVersion, PipelineVersion)
	}
	if d.DupCluster.IsZero() {
		t.Error("dup_cluster is unset, so a duplicate of this document could not be joined to it")
	}
	if !d.IsRepresentative {
		t.Error("the first copy of a document is the representative one")
	}
	if d.DupClusterSize != 0 {
		t.Errorf("dup_cluster_size is %d, and a streaming pass cannot know it, so it must stay zero rather than lie", d.DupClusterSize)
	}
	if d.LangScore <= 0 || d.LangScore > 1 {
		t.Errorf("lang_score is %v, outside (0, 1]", d.LangScore)
	}
	// Identity is blake3 of the text that ships, and the line changed the text,
	// so the identifier the ingest wrote is not the identifier this row carries.
	// That is the point: normalization puts a trailing newline on this article
	// and nothing downstream should be hashing the version without it.
	if d.DocID != doc.SumString(d.Text) {
		t.Error("doc_id is not blake3 of the text it ships with")
	}
	if d.DocID == before {
		t.Error("doc_id did not move, and normalization changed the text under it")
	}
}

func TestLineMovesIdentityWithTheText(t *testing.T) {
	// A page typed with a soft hyphen and a full width digit, which is what a
	// lot of this corpus looks like before phoi runs.
	dirty := strings.Replace(article, "39 độ C", "3​9 độ C", 1)
	l := New(1024)
	d := raw(dirty)
	before := d.DocID

	v := l.Run(d)
	if !v.Kept {
		t.Fatalf("the article was removed at %s for %s", v.Stage, v.Reason)
	}
	if d.DocID == before {
		t.Error("the line changed the text and left doc_id alone, so identity points at a document that no longer exists")
	}
	if d.DocID != doc.SumString(d.Text) {
		t.Error("doc_id is not blake3 of the text it ships with")
	}
	if d.RawID != doc.SumString("<html>"+dirty+"</html>") {
		t.Error("raw_id moved, and it is the column that still points at the upstream record")
	}
}

func TestLineDropsTheSecondCopy(t *testing.T) {
	l := New(1024)
	if v := l.Run(raw(article)); !v.Kept {
		t.Fatalf("the first copy was removed at %s for %s", v.Stage, v.Reason)
	}

	// The same page as a republisher would carry it: different case, different
	// punctuation. xay's key ignores exactly that, so it is the same document.
	again := raw(strings.ToUpper(article))
	v := l.Run(again)
	if v.Kept {
		t.Fatal("a republished copy of a document already admitted was kept")
	}
	if v.Stage != StageMill || v.Reason != vo.ReasonDuplicate {
		t.Errorf("the copy was removed at %s for %s, want %s for %s", v.Stage, v.Reason, StageMill, vo.ReasonDuplicate)
	}
	if again.DupCluster.IsZero() {
		t.Error("a dropped copy carries no cluster, so nothing can join it to the copy that was kept")
	}
	if again.IsRepresentative {
		t.Error("the copy that was dropped is marked as the representative one")
	}
}

func TestLineWithoutASetKeepsEveryCopy(t *testing.T) {
	// This is the ablation that measures what deduplication is worth, and it
	// only means anything if a nil set really turns the stage off.
	l := &Line{Limits: New(1).Limits, Level: che.L1}
	for i := range 3 {
		if v := l.Run(raw(article)); !v.Kept {
			t.Fatalf("copy %d was removed at %s for %s with deduplication off", i, v.Stage, v.Reason)
		}
	}
}

func TestLineSiftsWhatIsNotVietnameseProse(t *testing.T) {
	cases := map[string]string{
		"a caption": "Ảnh: Nguyễn Văn A.",
		"a menu":    strings.Repeat("Trang chủ | Tin tức | Thể thao | Giải trí | Liên hệ\n", 12),
		"another language": "The weather in Hanoi is hot this week, and the power grid is under strain. " +
			"Residents have been advised to stay indoors between eleven in the morning and three in the afternoon, " +
			"to drink water, and to look after the elderly and the very young in the household during the heat.",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			l := New(1024)
			v := l.Run(raw(text))
			if v.Kept {
				t.Fatalf("%s was published as Vietnamese prose", name)
			}
			if v.Stage != StageSift && v.Stage != StageNormalize {
				t.Errorf("removed at %s for %s, want the sift or the normalizer", v.Stage, v.Reason)
			}
		})
	}
}

func TestLineCoversPersonalDataAndDropsTheSpans(t *testing.T) {
	text := article + "\n\nLiên hệ toà soạn qua email bientap@vnexpress.net hoặc gọi 0912345678."
	l := New(1024)
	d := raw(text)

	v := l.Run(d)
	if !v.Kept {
		t.Fatalf("the article was removed at %s for %s", v.Stage, v.Reason)
	}
	if len(v.Found) == 0 {
		t.Fatal("an address and a phone number went through unfound")
	}
	if strings.Contains(d.Text, "bientap@vnexpress.net") || strings.Contains(d.Text, "0912345678") {
		t.Error("the identifiers are still in the text that ships")
	}
	if len(d.PIITypes) == 0 {
		t.Error("pii_types is empty on a document that carried personal data")
	}
	if d.PIISpans != nil {
		t.Error("pii_spans is written, and its offsets are into text that no longer exists")
	}
	if d.PIILevel != doc.RedactionLevel(che.L1) {
		t.Errorf("pii_level is %q, want %q", d.PIILevel, doc.RedactionLevel(che.L1))
	}
}

func TestClusterIgnoresWhatARepublisherChanges(t *testing.T) {
	same := []string{
		article,
		strings.ToUpper(article),
		strings.ReplaceAll(article, " ", "  "),
		strings.ReplaceAll(article, ",", ""),
	}
	want := Cluster(same[0])
	for i, text := range same[1:] {
		if got := Cluster(text); got != want {
			t.Errorf("variant %d clusters as %s, want %s", i+1, got, want)
		}
	}
	if Cluster(article) == Cluster(strings.Replace(article, "Hà Nội", "Đà Nẵng", 1)) {
		t.Error("two different articles cluster together")
	}
}

func TestKindsAreDistinctAndOrdered(t *testing.T) {
	text := "Gọi 0912345678 hoặc 0987654321, email a@b.vn."
	found := che.Find(text)
	got := kinds(found)
	if len(got) == 0 {
		t.Fatal("nothing was found in a line with two phone numbers and an address")
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Fatalf("pii_types repeats a kind: %v", got)
		}
	}
	if kinds(nil) != nil {
		t.Error("a document with no personal data carries an empty list rather than none")
	}
}

func TestCleanIsInTheHub(t *testing.T) {
	d := Clean()
	if d.Name != "vitco-clean" {
		t.Fatalf("the clean dataset is %q", d.Name)
	}
	if !d.Text {
		t.Error("the clean dataset withholds text, which is the one column it exists to publish")
	}
}
