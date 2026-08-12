package giao

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "readings.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadingsAreReadInTheOrderTheyWereWritten(t *testing.T) {
	path := write(t,
		`{"box":"server1","bytes":11000000000,"seconds":183,"measured_on":"2026-08-03","how":"an hour of the hplt3 ingest"}`,
		``,
		`{"box":"gamingpc","bytes":4200000000,"seconds":2266,"measured_on":"2026-08-03","how":"the dem count of one shard"}`,
	)

	got, err := ReadReadings(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d readings from two lines and a blank one", len(got))
	}
	if got[0].Box != "server1" || got[1].Box != "gamingpc" {
		t.Errorf("read %s then %s", got[0].Box, got[1].Box)
	}
	if got[0].Seconds != 183 || got[1].Bytes != 4200000000 {
		t.Errorf("read %+v", got)
	}
}

func TestAFieldTheScheduleDoesNotKnowAboutIsAnError(t *testing.T) {
	// The whole reason to refuse it: somebody adds a column, the schedule keeps
	// reading, and the plan that comes out is built from part of the file.
	path := write(t, `{"box":"server1","bytes":11000000000,"seconds":183,"measured_on":"2026-08-03","how":"ingest","threads":8}`)

	_, err := ReadReadings(path)
	if err == nil {
		t.Fatal("a reading with a field nobody reads was accepted")
	}
	if !strings.Contains(err.Error(), "threads") {
		t.Errorf("the error does not name the field: %v", err)
	}
	if !strings.Contains(err.Error(), ":1:") {
		t.Errorf("the error does not say which line: %v", err)
	}
}

func TestABadLineIsNamedByItsLineNumber(t *testing.T) {
	path := write(t,
		`{"box":"server1","bytes":11000000000,"seconds":183,"measured_on":"2026-08-03","how":"ingest"}`,
		`{"box":"server3",`,
	)

	_, err := ReadReadings(path)
	if err == nil {
		t.Fatal("half a line of JSON was accepted")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("the error does not point at line 2: %v", err)
	}
}

func TestAFileThatIsNotThereSaysSo(t *testing.T) {
	if _, err := ReadReadings(filepath.Join(t.TempDir(), "nothing.jsonl")); err == nil {
		t.Fatal("a file that does not exist read as no readings")
	}
}
