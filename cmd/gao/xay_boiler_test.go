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

// The furniture of two made up sites, written the way an extractor hands it
// over: the nav column first, the article, then the notice at the foot.
const (
	boilerNotice = "Bản quyền thuộc về báo điện tử. Nghiêm cấm sao chép dưới mọi hình thức."
	boilerShare  = "Chia sẻ bài viết này lên mạng xã hội"
)

// hostedPage is one page of a site: the same furniture around a different body.
func hostedPage(body string) string {
	return strings.Join([]string{"Trang chủ", "Thời sự", body, boilerShare, boilerNotice}, "\n") + "\n"
}

// hostedRow is a document with a host on it and text the caller chose, which is
// what a boilerplate pass reads and what the other fixtures in this package do
// not carry.
type hostedRow struct {
	host string
	text string
}

func writeHostedPart(t *testing.T, rows ...hostedRow) string {
	t.Helper()
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
	for i, r := range rows {
		row := document(t, i)
		row.Text = r.text
		row.DocID = doc.SumString(r.text)
		row.NChars = uint32(len([]rune(r.text)))
		row.Host = r.host
		row.URL = "https://" + r.host + "/bai-viet-" + string(rune('a'+i%26)) + ".html"
		if err := part.Append(row); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	file, err := part.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	return filepath.Join(dir, file.Path)
}

// bodyLines are the only lines that differ from page to page.
var bodyLines = []string{
	"Hà Nội bước vào đợt nắng nóng đầu tiên của mùa hè.",
	"Sông Hồng bắt nguồn từ Vân Nam và chảy qua các tỉnh phía bắc.",
	"Năm 1986, đại hội đảng quyết định chuyển sang nền kinh tế nhiều thành phần.",
	"Giá vé tàu tết được công bố sớm hơn mọi năm.",
	"Đội tuyển thắng hai trận liên tiếp trên sân nhà.",
	"Kỳ thi tốt nghiệp năm nay giữ nguyên bốn môn.",
}

func boilerPart(t *testing.T) string {
	t.Helper()
	rows := make([]hostedRow, 0, len(bodyLines)+1)
	for _, body := range bodyLines {
		rows = append(rows, hostedRow{host: "vnbao.vn", text: hostedPage(body)})
	}
	// One forum post that happens to end with the same sentence one of the
	// sites puts under every article.
	rows = append(rows, hostedRow{host: "diendan.vn", text: "Mình vừa đọc xong bài này.\n" + boilerShare + "\n"})
	return writeHostedPart(t, rows...)
}

// The headline number: how much of a part was the same four lines repeated on
// every page of one site.
func TestXayBoilerCountsWhatEveryPageOfAHostCarries(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-boiler", "-json", boilerPart(t)}); code != 0 {
		t.Fatalf("gao xay -boiler = %d, want 0\n%s", code, stderr.String())
	}
	var got boilerRun
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Hosts != 2 {
		t.Errorf("counted %d hosts, want 2", got.Hosts)
	}
	if got.Documents != 7 {
		t.Errorf("counted %d documents, want 7", got.Documents)
	}
	// Six pages of five lines and one post of two.
	if got.Lines != 32 {
		t.Errorf("counted %d lines, want 32", got.Lines)
	}
	if got.Removed != 24 {
		t.Errorf("removed %d lines, want the four furniture lines off each of six pages", got.Removed)
	}
	if got.Emptied != 0 {
		t.Errorf("%d documents were emptied, want 0", got.Emptied)
	}
	if len(got.Sites) != 2 || got.Sites[0].Host != "vnbao.vn" {
		t.Fatalf("the report leads with %+v, want the site the pass did the most to", got.Sites)
	}
	if got.Sites[1].Removed != 0 {
		t.Errorf("the forum lost %d lines, want 0, since one post repeating a sentence repeats nothing", got.Sites[1].Removed)
	}
}

// The thresholds go out with the numbers, because they are defaults rather than
// findings and a reader who does not know them cannot argue with the counts.
func TestXayBoilerSaysWhatItCountedAsFurniture(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-boiler", boilerPart(t)}); code != 0 {
		t.Fatalf("gao xay -boiler = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"vnbao.vn", "diendan.vn", "24 of 32 lines were furniture"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not mention %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, "appears in 3 of them or in 10.0%") {
		t.Errorf("the report does not say what made a line furniture\n%s", out)
	}
}

// A page that is nothing but furniture goes to the reject store rather than out
// of the corpus quietly, so the run has to say how many there were.
func TestXayBoilerReportsThePagesItEmptied(t *testing.T) {
	rows := make([]hostedRow, 0, len(bodyLines)+1)
	for _, body := range bodyLines {
		rows = append(rows, hostedRow{host: "vnbao.vn", text: hostedPage(body)})
	}
	rows = append(rows, hostedRow{host: "vnbao.vn", text: hostedPage("")})
	path := writeHostedPart(t, rows...)

	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-boiler", path}); code != 0 {
		t.Fatalf("gao xay -boiler = %d, want 0\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 documents were nothing but furniture") {
		t.Errorf("the run does not report the page it emptied\n%s", stdout.String())
	}
}

// Boilerplate is found per host, and a text file does not carry one. Reading it
// anyway would count every file as one nameless site and report that nothing
// repeats.
func TestXayBoilerRefusesATextFile(t *testing.T) {
	dir := t.TempDir()
	path := writeText(t, dir, "a.txt", xayArticle)

	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-boiler", path}); code != 2 {
		t.Fatalf("gao xay -boiler a.txt = %d, want 2\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "does not carry") {
		t.Errorf("it does not say why: %s", stderr.String())
	}
}

// Two measurements, one run each. Asking for both would print a curve of
// thresholds that had nothing to do with the furniture beside it.
func TestXayBoilerAndCurveAreNotRunTogether(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-boiler", "-curve", boilerPart(t)}); code != 2 {
		t.Fatalf("gao xay -boiler -curve = %d, want 2\n%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "two different measurements") {
		t.Errorf("it does not say why: %s", stderr.String())
	}
}

// A part off the fleet holds more hosts than anybody reads down, so the table is
// cut and says so.
func TestXayBoilerCutsTheTableAndSaysSo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runXay(&stdout, &stderr, []string{"-boiler", "-hosts", "1", boilerPart(t)}); code != 0 {
		t.Fatalf("gao xay -boiler -hosts 1 = %d, want 0\n%s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "diendan.vn") {
		t.Errorf("the second host is in a table cut to one\n%s", out)
	}
	if !strings.Contains(out, "1 more hosts are not shown") {
		t.Errorf("the table was cut without saying so\n%s", out)
	}
}
