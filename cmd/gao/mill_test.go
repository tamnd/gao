package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamnd/gao/doc"
	"github.com/tamnd/gao/store"
)

// One article, and the shape it turns up in on other sites. The body is what
// carries between them, which is exactly what the stage is built to notice.
const (
	millArticle = "Hà Nội sẽ mở rộng mạng lưới đường sắt đô thị trong mười năm tới. " +
		"Thành phố cho biết tuyến số ba chạy từ ga Hà Nội đến Hoàng Mai, và phần lớn kinh phí đến từ vốn vay ưu đãi. " +
		"Người dân dọc tuyến sẽ được hỏi ý kiến trước khi giải phóng mặt bằng, và việc đền bù sẽ theo khung giá mới. " +
		"Sở Giao thông vận tải nói rằng thiết kế cơ sở đã hoàn thành và hồ sơ mời thầu sẽ phát hành vào quý sau."

	millSyndicated = "Thủ đô chi thêm cho đường sắt đô thị. " +
		"Hà Nội sẽ mở rộng mạng lưới đường sắt đô thị trong mười năm tới. " +
		"Thành phố cho biết tuyến số ba chạy từ ga Hà Nội đến Hoàng Mai, và phần lớn kinh phí đến từ vốn vay ưu đãi. " +
		"Người dân dọc tuyến sẽ được hỏi ý kiến trước khi giải phóng mặt bằng, và việc đền bù sẽ theo khung giá mới. " +
		"Sở Giao thông vận tải nói rằng thiết kế cơ sở đã hoàn thành và hồ sơ mời thầu sẽ phát hành vào quý sau. " +
		"Theo TTXVN."

	millRiver = "Mùa nước nổi năm nay về muộn hơn mọi năm ở đầu nguồn sông Cửu Long. " +
		"Nông dân ở An Giang nói cá linh ít hơn hẳn, và những cánh đồng ngập chỉ còn chừng nửa mét nước. " +
		"Các nhà nghiên cứu cho rằng chuỗi đập thủy điện phía thượng nguồn đã giữ lại phần lớn phù sa. " +
		"Không có phù sa thì đất bạc màu, và vụ lúa sau đó phải bù bằng phân bón."
)

func writeText(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The headline number, on the case the stage exists for: one article on two
// sites and one article about something else is two documents, not three.
func TestMillCountsTheCopiesAndTheDocumentsLeft(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "a.txt", millArticle),
		writeText(t, dir, "b.txt", millSyndicated),
		writeText(t, dir, "c.txt", millRiver),
	}

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, append([]string{"-json"}, files...)); code != 0 {
		t.Fatalf("gao mill -json = %d, want 0\n%s", code, stderr.String())
	}
	var got millRun
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Report.Documents != 3 {
		t.Errorf("three files came to %d documents", got.Report.Documents)
	}
	if got.Report.Near != 1 {
		t.Errorf("the report found %d near copies, want the syndicated one", got.Report.Near)
	}
	if got.Report.Kept != 2 {
		t.Errorf("the report keeps %d documents, want the article and the one about the river", got.Report.Kept)
	}
}

// A number without the conditions it was measured under is not a measurement,
// so the table says what banding it ran at and what that banding catches.
func TestMillSaysWhatItRanAt(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "a.txt", millArticle),
		writeText(t, dir, "b.txt", millSyndicated),
	}

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, files); code != 0 {
		t.Fatalf("gao mill = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"documents", "exact", "near", "kept", "retention", "clusters"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report has no %s column:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "16 bands of 8 rows") {
		t.Errorf("the report does not say what banding it ran at:\n%s", out)
	}
	if !strings.Contains(out, "0.71") {
		t.Errorf("the report does not say what threshold it ran at:\n%s", out)
	}
}

// A part is what the ingest writes, so reading one is how this stage sees a
// corpus rather than a directory somebody assembled by hand.
func TestMillReadsAParquetPart(t *testing.T) {
	d, ok := store.Lookup("vietnamese-web-text")
	if !ok {
		t.Fatal("the dataset is not in the table")
	}
	dir := t.TempDir()
	part, err := store.CreatePart(dir, "part-00000.parquet", d, store.Stamp{
		Snapshot: "gao-v1.0", Stage: "test@0.1.0", Box: "server1",
	})
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	// The same document twice is a scraper that ran twice, which is the
	// commonest thing a part holds more than one copy of.
	one := document(t, 0)
	other := document(t, 1)
	other.Text = millRiver
	other.DocID = doc.SumString(other.Text)
	other.NChars = uint32(len([]rune(other.Text)))
	for _, row := range []*doc.Document{one, one, other} {
		if err := part.Append(row); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	file, err := part.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, []string{"-json", filepath.Join(dir, file.Path)}); code != 0 {
		t.Fatalf("gao mill PART = %d, want 0\n%s", code, stderr.String())
	}
	var got millRun
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Report.Documents != 3 {
		t.Errorf("the part holds three rows and the report counted %d", got.Report.Documents)
	}
	if got.Report.Exact != 1 {
		t.Errorf("one row was written twice and the report found %d exact copies", got.Report.Exact)
	}
	if got.Report.Kept != 2 {
		t.Errorf("the report keeps %d documents, want 2", got.Report.Kept)
	}
}

// The curve is the deliverable of this stage, so it has to be one command away
// and it has to name every threshold it was taken at.
func TestMillPrintsTheAblationCurve(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "a.txt", millArticle),
		writeText(t, dir, "b.txt", millSyndicated),
		writeText(t, dir, "c.txt", millRiver),
	}

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, append([]string{"-curve", "-json"}, files...)); code != 0 {
		t.Fatalf("gao mill -curve = %d, want 0\n%s", code, stderr.String())
	}
	var got millCurve
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the curve is not JSON: %v\n%s", err, stdout.String())
	}
	if len(got.Curve) != 8 {
		t.Fatalf("the curve has %d points, want 8", len(got.Curve))
	}
	for i := 1; i < len(got.Curve); i++ {
		if got.Curve[i].Kept < got.Curve[i-1].Kept {
			t.Errorf("threshold %.2f keeps %d and the stricter %.2f keeps %d",
				got.Curve[i-1].Threshold, got.Curve[i-1].Kept, got.Curve[i].Threshold, got.Curve[i].Kept)
		}
	}
	if got.Banding.Bands != 32 || got.Banding.Rows != 4 {
		t.Errorf("the curve was taken at %d bands of %d rows, want the wide banding a low threshold can be seen through",
			got.Banding.Bands, got.Banding.Rows)
	}
}

// The curve is a table of thresholds and what each of them costs, and a reader
// who cannot see the numbers cannot use it.
func TestMillPrintsTheCurveAsATable(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		writeText(t, dir, "a.txt", millArticle),
		writeText(t, dir, "b.txt", millSyndicated),
	}

	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, append([]string{"-curve"}, files...)); code != 0 {
		t.Fatalf("gao mill -curve = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"threshold", "0.50", "0.95", "retention"} {
		if !strings.Contains(out, want) {
			t.Errorf("the curve does not print %q:\n%s", want, out)
		}
	}
}

// A similarity is between nothing in common and the same document. A number
// outside that is a typed mistake, and running anyway would produce a report
// that looks like an answer.
func TestMillRefusesAThresholdThatIsNotASimilarity(t *testing.T) {
	path := writeText(t, t.TempDir(), "a.txt", millArticle)
	for _, bad := range []string{"-threshold=70", "-threshold=-0.1"} {
		var stdout, stderr bytes.Buffer
		if code := runMill(&stdout, &stderr, []string{bad, path}); code != 2 {
			t.Errorf("gao mill %s = %d, want 2", bad, code)
		}
		if !strings.Contains(stderr.String(), "between 0 and 1") {
			t.Errorf("the error for %s does not say what a threshold is: %q", bad, stderr.String())
		}
	}
}

func TestMillSaysWhenItWasGivenNothingToRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, nil); code != 2 {
		t.Fatalf("gao mill with no files = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "parquet parts or text files") {
		t.Errorf("the error does not say what it wants: %q", stderr.String())
	}
}

func TestMillSaysWhichFileItCouldNotRead(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runMill(&stdout, &stderr, []string{filepath.Join(t.TempDir(), "gone.txt")}); code != 1 {
		t.Fatalf("gao mill = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gone.txt") {
		t.Errorf("the error does not name the file: %q", stderr.String())
	}
}

func TestMillIsInTheUsage(t *testing.T) {
	var stdout bytes.Buffer
	usage(&stdout)
	if !strings.Contains(stdout.String(), "mill") {
		t.Errorf("gao help does not list xay:\n%s", stdout.String())
	}
}
