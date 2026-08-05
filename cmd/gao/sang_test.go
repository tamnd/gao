package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/kho"
)

// One article of the length this stage is built to keep, and the two shapes a
// page takes when it is not one. The article is what has to survive.
const (
	sangArticle = "Bộ Giao thông vận tải vừa trình phương án nâng cấp tuyến quốc lộ chạy qua bốn tỉnh miền Trung.\n" +
		"Theo phương án này, đoạn qua Quảng Nam sẽ được mở rộng lên bốn làn xe và phần lớn kinh phí đến từ vốn vay ưu đãi.\n" +
		"Đại diện của bộ cho biết thiết kế cơ sở đã hoàn thành và hồ sơ mời thầu sẽ phát hành vào quý sau.\n" +
		"Người dân dọc tuyến sẽ được hỏi ý kiến trước khi giải phóng mặt bằng, và việc đền bù sẽ theo khung giá mới.\n" +
		"Nhiều hộ kinh doanh nói rằng họ muốn biết sớm để còn thu xếp chỗ bán hàng trong thời gian thi công."

	sangCaption = "Người dân đi lại trên phố Hàng Bài chiều 12 tháng 8. Ảnh: Ngọc Thành."

	// A shop notice: too short to be a document at the default floor, and
	// ordinary Vietnamese in every other respect, which is what makes it the
	// fixture the floor can be moved under.
	sangNotice = "Cửa hàng của chúng tôi mở cửa từ bảy giờ sáng và đóng cửa lúc chín giờ tối, kể cả ngày lễ và chủ nhật."

	sangMenu = "- Trang chủ\n- Thời sự\n- Góc nhìn\n- Thế giới\n- Kinh doanh\n- Bất động sản\n" +
		"- Khoa học\n- Giải trí\n- Thể thao\n- Pháp luật\n- Giáo dục\n- Sức khỏe\n- Đời sống\n" +
		"- Du lịch\n- Số hóa\n- Xe\n- Ý kiến\n- Tâm sự\n- Hài\n- Video\n- Ảnh\n- Infographics\n" +
		"- Podcast\n- Tất cả chuyên mục\n- Đăng nhập\n- Đăng ký\n- Liên hệ tòa soạn\n- Quảng cáo\n" +
		"- Điều khoản sử dụng\n- Chính sách bảo mật"

	// English of about the length of the article, so that what separates the two
	// is the language and not the size. A crawl seeded on Vietnamese domains
	// returns a great deal of this.
	sangEnglish = "The committee met for the third time this year to review the proposal, and after a long " +
		"discussion it decided to postpone the vote until the next session.\n" +
		"Several members argued that the report had arrived too late for anyone to read it properly.\n" +
		"The chair replied that the deadline had been set by the ministry and was not hers to move.\n" +
		"A revised draft will be circulated before the end of the month, together with the minutes of " +
		"the two meetings that preceded this one."
)

// The headline numbers, on the three documents the stage exists to tell apart.
func TestSangKeepsTheArticleAndSaysWhyItDroppedTheRest(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "article.txt", sangArticle),
		writeText(t, dir, "caption.txt", sangCaption),
		writeText(t, dir, "menu.txt", sangMenu),
	}

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, append([]string{"-json"}, files...)); code != 0 {
		t.Fatalf("gao sang -json = %d, want 0\n%s", code, stderr.String())
	}
	var got sangReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Total.Documents != 3 || got.Total.Kept != 1 {
		t.Fatalf("three files came to %d documents with %d kept, want 3 and 1", got.Total.Documents, got.Total.Kept)
	}
	if got.Total.Rejected["short"] != 1 {
		t.Errorf("a caption was counted as %v", got.Total.Rejected)
	}
	if got.Total.Rejected["boilerplate"] != 1 {
		t.Errorf("a navigation bar was counted as %v", got.Total.Rejected)
	}
}

// A rejection nobody can trace is a rejection nobody can argue with, so every
// row carries the value that failed beside the bound it failed against.
func TestSangSaysWhichNumberDroppedEachDocument(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "article.txt", sangArticle),
		writeText(t, dir, "caption.txt", sangCaption),
	}

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, append([]string{"-json"}, files...)); code != 0 {
		t.Fatalf("gao sang = %d, want 0\n%s", code, stderr.String())
	}
	var got sangReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	for _, row := range got.Documents {
		if strings.HasSuffix(row.Document, "article.txt") {
			if !row.Kept {
				t.Errorf("the article was dropped as %s: %s", row.Reason, row.Detail)
			}
			continue
		}
		if row.Kept {
			t.Fatal("the caption went through")
		}
		if !strings.Contains(row.Detail, "60") {
			t.Errorf("the detail is %q and does not name the bound", row.Detail)
		}
	}
}

// The row carries measurements rather than verdicts, because a corpus that only
// recorded that a document passed cannot be re-filtered at another threshold
// without the text, which is off the box by then.
func TestSangPutsTheMeasurementsOnTheRow(t *testing.T) {
	path := writeText(t, t.TempDir(), "article.txt", sangArticle)

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, []string{"-json", path}); code != 0 {
		t.Fatalf("gao sang = %d, want 0\n%s", code, stderr.String())
	}
	var got sangReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	if len(got.Documents) != 1 {
		t.Fatalf("one file came to %d rows", len(got.Documents))
	}
	row := got.Documents[0]
	if row.Syllables < 60 {
		t.Errorf("the article measured %d syllables, which is not the fixture this file describes", row.Syllables)
	}
	if row.Mean < 3.0 || row.Mean > 3.6 {
		t.Errorf("the mean syllable is %.2f letters, which is not Vietnamese", row.Mean)
	}
	if row.StopWords < 2 {
		t.Errorf("the article holds %d distinct function words", row.StopWords)
	}
	if row.Diacritics != "present" {
		t.Errorf("a marked article is labeled %q", row.Diacritics)
	}
}

// English of the same length as the article has to be filed under language and
// not under one of the shape checks, because a reject store where the reasons
// are approximately right cannot answer what any threshold cost.
func TestSangFilesAnotherLanguageUnderLanguage(t *testing.T) {
	path := writeText(t, t.TempDir(), "english.txt", sangEnglish)

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, []string{"-json", path}); code != 0 {
		t.Fatalf("gao sang = %d, want 0\n%s", code, stderr.String())
	}
	var got sangReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	if got.Total.Rejected["language"] != 1 {
		t.Fatalf("English was filed as %v", got.Total.Rejected)
	}
	if row := got.Documents[0]; row.Syllables < 60 {
		t.Errorf("the fixture measured %d syllables, so it was dropped for its length before the language was looked at", row.Syllables)
	}
}

// The shares the language verdict was taken on go on the row beside everything
// else, for the reason every other measurement does: the identifier is the part
// of this stage most likely to have its threshold moved after somebody reads
// what it let through.
func TestSangPutsTheLanguageSharesOnTheRow(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "article.txt", sangArticle),
		writeText(t, dir, "english.txt", sangEnglish),
	}

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, append([]string{"-json"}, files...)); code != 0 {
		t.Fatalf("gao sang = %d, want 0\n%s", code, stderr.String())
	}
	var got sangReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	rows := map[string]sangRow{}
	for _, r := range got.Documents {
		rows[filepath.Base(r.Document)] = r
	}
	article, english := rows["article.txt"], rows["english.txt"]
	if article.Vietnamese < 0.9 {
		t.Errorf("%.3f of the article is Vietnamese syllables", article.Vietnamese)
	}
	if article.MarkRate < 0.5 {
		t.Errorf("the article carries marks on %.3f of its tokens, so it is not the marked fixture this test needs", article.MarkRate)
	}
	if english.Vietnamese > 0.5 || english.BareRate > 0.5 {
		t.Errorf("English measured %.3f syllables and %.3f with the marks off", english.Vietnamese, english.BareRate)
	}
	if article.BareRate < article.Vietnamese {
		t.Errorf("taking the marks off matched less than leaving them on, %.3f against %.3f", article.BareRate, article.Vietnamese)
	}
}

// A part is what the ingest writes, so reading one is how this stage sees a
// corpus rather than a directory somebody assembled by hand.
func TestSangReadsAParquetPart(t *testing.T) {
	d, ok := kho.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the dataset is not in the table")
	}
	dir := t.TempDir()
	part, err := kho.CreatePart(dir, "part-00000.parquet", d, kho.Stamp{
		Snapshot: "gao-v1.0", Stage: "test@0.1.0", Box: "server1",
	})
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	rows := []string{sangArticle, sangCaption}
	for i, text := range rows {
		row := document(t, i)
		row.Text = text
		row.DocID = doc.SumString(text)
		row.NChars = uint32(len([]rune(text)))
		if err := part.Append(row); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	file, err := part.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, []string{"-json", filepath.Join(dir, file.Path)}); code != 0 {
		t.Fatalf("gao sang PART = %d, want 0\n%s", code, stderr.String())
	}
	var got sangReport
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Total.Documents != 2 {
		t.Errorf("the part holds two rows and the report counted %d", got.Total.Documents)
	}
	if got.Total.Kept != 1 {
		t.Errorf("the report kept %d of them, want the article", got.Total.Kept)
	}
	if len(got.Documents) != 2 {
		t.Fatalf("the report has %d rows", len(got.Documents))
	}
	for i, row := range got.Documents {
		if !strings.HasSuffix(row.Document, "#"+string(rune('0'+i))) {
			t.Errorf("row %d is named %q, and a row inside a part has to say which row it is", i, row.Document)
		}
	}
}

// The table is what somebody reads when they suspect a threshold is removing
// good documents, and it is only useful if it prints what was measured.
func TestSangPrintsTheMeasurementsAsATable(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "article.txt", sangArticle),
		writeText(t, dir, "menu.txt", sangMenu),
	}

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, files); code != 0 {
		t.Fatalf("gao sang = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"syllables", "mean", "vietnamese", "diacritics", "boilerplate", "next stage"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not print %q:\n%s", want, out)
		}
	}
}

// Every threshold in this stage is one an ablation is expected to move, and the
// length floor is the one that moves most, so it is on the command line.
func TestSangLetsTheLengthFloorBeMoved(t *testing.T) {
	path := writeText(t, t.TempDir(), "notice.txt", sangNotice)

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, []string{"-json", path}); code != 0 {
		t.Fatalf("gao sang = %d, want 0\n%s", code, stderr.String())
	}
	var atDefault sangReport
	if err := json.Unmarshal(stdout.Bytes(), &atDefault); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	if atDefault.Total.Rejected["short"] != 1 {
		t.Fatalf("the notice was not dropped for its length, which is what this test moves: %v", atDefault.Documents)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runSang(&stdout, &stderr, []string{"-min-syllables=5", "-json", path}); code != 0 {
		t.Fatalf("gao sang -min-syllables=5 = %d, want 0\n%s", code, stderr.String())
	}
	var lowered sangReport
	if err := json.Unmarshal(stdout.Bytes(), &lowered); err != nil {
		t.Fatalf("the report is not JSON: %v", err)
	}
	if lowered.Total.Kept != 1 {
		t.Errorf("the notice is still dropped after the floor was lowered under it: %v", lowered.Documents)
	}
}

func TestSangRefusesALengthFloorThatIsNotALength(t *testing.T) {
	path := writeText(t, t.TempDir(), "article.txt", sangArticle)

	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, []string{"-min-syllables=-1", path}); code != 2 {
		t.Fatalf("gao sang -min-syllables=-1 = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "shorter than no syllables") {
		t.Errorf("the error does not say what is wrong with it: %q", stderr.String())
	}
}

func TestSangSaysWhenItWasGivenNothingToRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, nil); code != 2 {
		t.Fatalf("gao sang with no files = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "parquet parts or text files") {
		t.Errorf("the error does not say what it wants: %q", stderr.String())
	}
}

func TestSangSaysWhichFileItCouldNotRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runSang(&stdout, &stderr, []string{filepath.Join(t.TempDir(), "gone.txt")}); code != 1 {
		t.Fatalf("gao sang = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gone.txt") {
		t.Errorf("the error does not name the file: %q", stderr.String())
	}
}

func TestSangIsInTheUsage(t *testing.T) {
	var stdout bytes.Buffer
	usage(&stdout)
	if !strings.Contains(stdout.String(), "sang") {
		t.Errorf("gao help does not list sang:\n%s", stdout.String())
	}
}
