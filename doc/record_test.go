package doc

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordJSONIsFlat(t *testing.T) {
	// The field groups are embedded rather than nested so the JSON stays flat.
	// The Parquet release depends on that, and so does anyone reading a segment
	// with grep. If someone changes an embedded group to a named field, the
	// column names all change and this test is the alarm.
	b, err := json.Marshal(valid())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal into a map: %v", err)
	}
	for _, col := range []string{
		"doc_id", "raw_id", "text", "schema_version",
		"source", "source_locator", "url", "host", "fetched_at",
		"media_type", "extractor", "pipeline_version",
		"lang", "lang_score", "diacritics", "translated",
		"gao_qual", "gao_edu", "is_representative",
		"pii_level", "license_class", "license_evidence",
		"n_chars", "n_syllables", "n_tokens",
	} {
		if _, ok := m[col]; !ok {
			t.Errorf("column %q is missing from the flat record", col)
		}
	}
	for _, group := range []string{"Provenance", "Language", "Quality", "Dedup", "Privacy", "Licensing", "Shape"} {
		if _, ok := m[group]; ok {
			t.Errorf("group %q leaked into the JSON as a nested object", group)
		}
	}
}

func TestRecordRoundTrips(t *testing.T) {
	in := valid()
	in.Heuristics = map[string]float32{"mean_line_len": 61.4, "punct_ratio": 0.041}
	in.TDMSignals = map[string]string{"tdmrep": "1"}
	in.DupCluster = Cluster{0xde, 0xad, 0xbe, 0xef}
	in.DupClusterSize = 12
	in.ContamFlags = []string{"vmlu"}
	in.UpstreamFields = map[string]string{"hplt_doc_id": "vie_Latn/10_1:88214"}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Document
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.DocID != in.DocID || out.Text != in.Text {
		t.Error("identity or text did not survive the round trip")
	}
	if out.LicenseClass != in.LicenseClass {
		t.Errorf("license class round tripped to %s, want %s", out.LicenseClass, in.LicenseClass)
	}
	if out.DupCluster != in.DupCluster {
		t.Errorf("dup cluster round tripped to %s, want %s", out.DupCluster, in.DupCluster)
	}
	if out.Heuristics["mean_line_len"] != in.Heuristics["mean_line_len"] {
		t.Error("heuristics did not survive the round trip")
	}
	if err := out.Admit(); err != nil {
		t.Errorf("a round tripped document no longer satisfies the contract: %v", err)
	}
}

func TestEmptyOptionalFieldsStayOutOfTheRecord(t *testing.T) {
	// Half a billion rows makes an unused column expensive, so the optional
	// groups omit rather than emit nulls. The crawl fields in particular are
	// empty for every ingested corpus, which is most of the corpus.
	b, err := json.Marshal(valid())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, col := range []string{
		"http_status", "robots_decision", "robots_rule", "robots_hash",
		"tdm_signals", "hplt_bucket", "register", "heuristics",
		"dup_cluster", "pii_types", "pii_spans", "contam_flags", "upstream_fields",
	} {
		if strings.Contains(string(b), `"`+col+`"`) {
			t.Errorf("unset optional column %q was written anyway", col)
		}
	}
}

func TestLicenseClassMarshalsByName(t *testing.T) {
	// The name and not the ordinal, so that inserting a class later does not
	// silently reinterpret every existing row.
	b, err := json.Marshal(LicensePermissiveAttribution)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `"permissive-attribution"` {
		t.Errorf("license class marshaled as %s", b)
	}
	if _, err := json.Marshal(LicenseClass(99)); err == nil {
		t.Error("an undefined license class marshaled without complaint")
	}
	var c LicenseClass
	if err := json.Unmarshal([]byte(`"nope"`), &c); err == nil {
		t.Error("an unknown license name unmarshaled without complaint")
	}
}

func TestSyntheticIsNotNatural(t *testing.T) {
	// Natural and synthetic never mix in a headline. This is the single place
	// that rule is encoded, so that no report has to remember it.
	for _, s := range Sources() {
		if s == SourceSynth {
			if s.Natural() {
				t.Error("gao-synth counts as natural, which would put generated text in the corpus size")
			}
			continue
		}
		if !s.Natural() {
			t.Errorf("%s does not count as natural", s)
		}
	}
	if Source("made-up").Natural() {
		t.Error("an undefined source counts as natural")
	}
}
