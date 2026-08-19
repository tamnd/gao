package mill

import (
	"strings"
	"testing"
)

// A document long enough to have a signature worth comparing, built out of one
// pool of Vietnamese words in an order the subject decides. Two subjects give
// two documents that share vocabulary and almost no phrasing, which is what two
// unrelated pages off two sites look like. A fixture built from one template
// with the subject swapped into it would share most of its five-grams with every
// other fixture, and every test below would pass without measuring anything.
func story(subject string) string {
	pool := strings.Fields(`nhà nước quy định công dân thông tin trách nhiệm cơ quan bảo đảm quyền
	tiếp cận văn bản hồ sơ tài liệu giấy tờ thủ tục hành chính địa phương trung ương
	giáo dục đào tạo học sinh sinh viên trường lớp kỳ thi kết quả báo cáo rà soát
	thời hạn thực hiện đơn vị bộ phận thường trực đại diện giai đoạn chuẩn bị
	hiệu lực ban hành sửa đổi bổ sung hướng dẫn kiểm tra giám sát xử lý vi phạm`)

	seed := uint64(14695981039346656037)
	for _, r := range subject {
		seed = (seed ^ uint64(r)) * 1099511628211
	}
	var b strings.Builder
	for range 160 {
		seed = seed*6364136223846793005 + 1442695040888963407
		b.WriteString(pool[seed>>33%uint64(len(pool))])
		b.WriteByte(' ')
	}
	return b.String()
}

func overlap(t *testing.T, pairs ...[2]string) *Overlap {
	t.Helper()
	o, err := NewOverlap(Wide())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pairs {
		if _, err := o.Add(p[0], p[1]); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestSourcesThatShareNothingAddUpHonestly(t *testing.T) {
	o := overlap(t,
		[2]string{"hplt", story("tuyển sinh đại học")},
		[2]string{"hplt", story("học phí bậc trung học")},
		[2]string{"fineweb2", story("đăng ký xe máy điện")},
	)
	m := o.Matrix(0.8)

	if m.Union != 3 {
		t.Errorf("three unrelated documents made %d, want 3", m.Union)
	}
	if got := m.Inflation(); got != 1 {
		t.Errorf("disjoint sources inflate by %.2f, want 1.00, which is what adding them up assumes", got)
	}
	if got := m.Containment(0, 1); got != 0 {
		t.Errorf("hplt is %.1f%% inside fineweb2, want none of it", got)
	}
}

// The number the whole measurement exists to produce. Three sources holding the
// same document publish three documents and hold one.
func TestOneDocumentInThreeSourcesIsCountedOnce(t *testing.T) {
	shared := story("kỳ thi tốt nghiệp")
	o := overlap(t,
		[2]string{"hplt", shared},
		[2]string{"fineweb2", shared},
		[2]string{"culturax", shared},
	)
	m := o.Matrix(0.8)

	if m.Union != 1 {
		t.Errorf("one document in three sources came to %d distinct, want 1", m.Union)
	}
	if m.Sum != 3 {
		t.Errorf("the sources counted one at a time came to %d, want 3", m.Sum)
	}
	if got := m.Inflation(); got != 3 {
		t.Errorf("inflation is %.2f, want 3.00", got)
	}
	for i := range m.Sources {
		if got := m.Only[i]; got != 0 {
			t.Errorf("%s is the sole holder of %d documents, and it holds nothing the others do not", m.Sources[i], got)
		}
	}
}

// The asymmetry is the point. A small source almost entirely inside a large one
// buys nothing, and the same pair of counts read the other way looks harmless.
func TestContainmentIsReportedInBothDirectionsBecauseItIsNotSymmetric(t *testing.T) {
	small := story("quy chế thi")
	pairs := make([][2]string, 0, 11)
	pairs = append(pairs, [2]string{"glotcc", small}, [2]string{"hplt", small})
	for i := range 9 {
		pairs = append(pairs, [2]string{"hplt", story("chủ đề số " + string(rune('a'+i)))})
	}
	m := overlap(t, pairs...).Matrix(0.8)

	glot, hplt := 0, 1
	if m.Sources[glot] != "glotcc" || m.Sources[hplt] != "hplt" {
		t.Fatalf("sources came back as %v, want glotcc then hplt", m.Sources)
	}
	inHPLT := m.Containment(glot, hplt)
	inGlot := m.Containment(hplt, glot)
	if inHPLT != 100 {
		t.Errorf("%.1f%% of glotcc is in hplt, want all of it", inHPLT)
	}
	if inGlot >= 20 {
		t.Errorf("%.1f%% of hplt is in glotcc, want a small share, since hplt is ten times the size", inGlot)
	}
	if inHPLT == inGlot {
		t.Error("the two directions came back equal, which is the one thing a containment measure must not do")
	}
}

// Sole holding cannot be read off the shared counts, which is why it is counted
// where the memberships are still in hand.
func TestASourceIsCreditedOnlyForWhatNothingElseHolds(t *testing.T) {
	everywhere := story("lịch nghỉ lễ")
	o := overlap(t,
		[2]string{"hplt", everywhere},
		[2]string{"fineweb2", everywhere},
		[2]string{"culturax", everywhere},
		[2]string{"culturax", story("giấy phép lái xe hạng B")},
	)
	m := o.Matrix(0.8)

	for i, name := range m.Sources {
		want := 0
		if name == "culturax" {
			want = 1
		}
		if got := m.Only[i]; got != want {
			t.Errorf("%s is the sole holder of %d documents, want %d", name, got, want)
		}
	}
	culturax := 2
	if m.Sources[culturax] != "culturax" {
		t.Fatalf("sources came back as %v", m.Sources)
	}
	if got := m.Contribution(culturax); got != 50 {
		t.Errorf("culturax contributed %.1f%%, want 50.0%%, since one of its two documents is its own", got)
	}
}

// A source that hands over the same document twice holds it once, because the
// question here is what a source has and not how many times it says so.
func TestASourceRepeatingItselfIsCountedOnce(t *testing.T) {
	same := story("tuyển dụng viên chức")
	o := overlap(t, [2]string{"hplt", same}, [2]string{"hplt", same})

	if got := o.Documents("hplt"); got != 2 {
		t.Errorf("hplt handed over %d documents, want 2", got)
	}
	m := o.Matrix(0.8)
	if m.Distinct[0] != 1 {
		t.Errorf("hplt holds %d distinct documents, want 1", m.Distinct[0])
	}
	if m.Union != 1 || m.Sum != 1 {
		t.Errorf("union %d and sum %d, want 1 and 1", m.Union, m.Sum)
	}
}

// A near copy is the case worth having. Two sites that ran the same wire story
// under their own headlines hold one document, and identity alone cannot see it.
func TestANearCopyAcrossSourcesIsStillOneDocument(t *testing.T) {
	body := story("bảo hiểm y tế học sinh")
	o := overlap(t,
		[2]string{"hplt", "Tin trong nước. " + body},
		[2]string{"fineweb2", body + " Nguồn: bản tin buổi chiều."},
	)
	m := o.Matrix(0.7)

	if m.Union != 1 {
		t.Errorf("the same story under two headlines came to %d documents, want 1", m.Union)
	}
	if got := m.Containment(0, 1); got != 100 {
		t.Errorf("%.1f%% of the first source is in the second, want all of it", got)
	}
}

// The threshold is an argument rather than a constant, since the overlap between
// two sources is a different number depending on what counts as the same
// document, and hiding that behind one figure is how a matrix gets quoted wrong.
func TestTheAnswerMovesWithTheThreshold(t *testing.T) {
	body := story("chương trình giáo dục phổ thông")
	o := overlap(t,
		[2]string{"hplt", "Tin trong nước. " + body},
		[2]string{"fineweb2", body + " Nguồn: bản tin buổi chiều."},
	)
	loose := o.Matrix(0.6)
	strict := o.Matrix(0.999)

	if loose.Union >= strict.Union {
		t.Errorf("a loose threshold found %d documents and a strict one %d, and the loose one has to find fewer", loose.Union, strict.Union)
	}
	if loose.Sum != strict.Sum {
		t.Errorf("the per source counts moved with the threshold, %d against %d, and they do not depend on it", loose.Sum, strict.Sum)
	}
}

func TestAnEmptyMeasurementReportsNothingRatherThanDividingByZero(t *testing.T) {
	m := overlap(t).Matrix(0.8)
	if m.Union != 0 || m.Sum != 0 {
		t.Errorf("an empty measurement reports %d and %d", m.Union, m.Sum)
	}
	if m.Inflation() != 0 || m.Containment(0, 0) != 0 || m.Contribution(0) != 0 || m.Clusters(0) != 0 {
		t.Error("an empty measurement returned a number instead of nothing")
	}
}

func TestMoreSourcesThanAMembershipHoldsIsRefused(t *testing.T) {
	o, err := NewOverlap(Wide())
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxSources {
		if _, err := o.Add(string(rune('a'+i%26))+string(rune('a'+i/26)), story("x")); err != nil {
			t.Fatalf("source %d: %v", i, err)
		}
	}
	if _, err := o.Add("one-too-many", story("y")); err == nil {
		t.Errorf("a %dth source was accepted into a membership that holds %d", MaxSources+1, MaxSources)
	}
}

func TestTheMatrixSaysWhichWayToReadIt(t *testing.T) {
	shared := story("tuyển sinh")
	m := overlap(t,
		[2]string{"hplt", shared},
		[2]string{"glotcc", shared},
		[2]string{"hplt", story("một chủ đề khác")},
	).Matrix(0.8)

	out := m.String()
	for _, want := range []string{"hplt", "glotcc", "times over", "the source on the left"} {
		if !strings.Contains(out, want) {
			t.Errorf("the published matrix does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Error("the published matrix has an em dash in it")
	}
}
