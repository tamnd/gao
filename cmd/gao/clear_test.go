package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheRotationTheCrawlIsPlannedAgainstWorks(t *testing.T) {
	out, errOut, code := exec(t, "clear", "fit")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"server1", "scratch", "uplink", "confirm", "nothing may be deleted", "before fetching has to stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not mention %q:\n%s", want, out)
		}
	}
}

// An uplink slower than the crawl is a deadline rather than a warning, and the
// exit code has to say so because a pipeline reads that and not the prose.
func TestARotationThatDoesNotKeepUpExitsNonZero(t *testing.T) {
	out, _, code := exec(t, "clear", "fit", "-uplink", "1500000")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "no cleanup pass recovers that") {
		t.Errorf("the verdict does not say why cleaning up harder is not the answer:\n%s", out)
	}
}

// Half the value of this being arithmetic is that somebody can argue with a
// number and see the answer change.
func TestEveryNumberCanBeArguedWith(t *testing.T) {
	out, _, code := exec(t, "clear", "fit", "-confirm", "12h")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "12.0 hours") {
		t.Errorf("the confirmation lag that was passed in is not in the reading:\n%s", out)
	}

	out, _, code = exec(t, "clear", "fit", "-fetches", "50")
	if code != 0 {
		t.Fatalf("exit %d at a quarter of the fetch rate:\n%s", code, out)
	}
}

// A box with no scratch is a box that cannot hold the crawl, and server2 is on
// the fleet precisely so that this answer exists.
func TestABoxWithNoRoomSaysSoRatherThanPrintingAPlan(t *testing.T) {
	out, _, code := exec(t, "clear", "fit", "-box", "server2")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "before the first file is even closed") {
		t.Errorf("the reason is not given:\n%s", out)
	}
	if !strings.Contains(out, "  and ") {
		t.Errorf("only one of two reasons was printed:\n%s", out)
	}
}

func TestABoxThatIsNotOnTheFleetIsAUsageError(t *testing.T) {
	_, errOut, code := exec(t, "clear", "fit", "-box", "server9")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "not on the fleet inventory") {
		t.Errorf("the error does not say what is wrong: %s", errOut)
	}
}

func TestTheArithmeticIsAvailableAsJSON(t *testing.T) {
	out, errOut, code := exec(t, "clear", "fit", "-json")
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got clearFitReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Fits || got.Fills {
		t.Errorf("the planned rotation reads as %+v", got)
	}
	if got.Held <= 0 || got.Outage <= 0 || got.Mark >= got.Scratch {
		t.Errorf("the numbers do not hold together: %+v", got)
	}
}

// rotationLog writes a log of files that each took the states named, which is
// the shape the crawl is supposed to emit.
func rotationLog(t *testing.T, files map[string][]string) string {
	t.Helper()
	var b strings.Builder
	var minute int
	for name, states := range files {
		for _, state := range states {
			minute++
			b.WriteString(`{"name":"` + name + `","path":"data/gao-crawl-2026-09/` + name +
				`","bytes":1000000000,"hash":"1f4a` + name + `","state":"` + state +
				`","at":"2026-09-14T03:` + pad(minute) + `:00Z","box":"server1"}` + "\n")
		}
	}
	path := filepath.Join(t.TempDir(), "rotation.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func pad(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestARotationThatTookEveryStepReadsClean(t *testing.T) {
	path := rotationLog(t, map[string][]string{
		"part-00001.warc.gz": {"resident", "pushed", "verified", "reclaimed"},
	})
	out, errOut, code := exec(t, "clear", "read", path)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{"resident", "reclaimed", "1.0 GB freed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the reading does not mention %q:\n%s", want, out)
		}
	}
}

// This is what the command exists for. A box that deleted something it never
// confirmed looks exactly like a box that behaved, from the disk.
func TestALogThatDeletedSomethingUnconfirmedFails(t *testing.T) {
	path := rotationLog(t, map[string][]string{
		"part-00002.warc.gz": {"resident", "pushed", "reclaimed"},
	})
	out, _, code := exec(t, "clear", "read", path)
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "cannot be trusted") {
		t.Errorf("the verdict is too kind:\n%s", out)
	}
	if !strings.Contains(out, "the log cannot say which") {
		t.Errorf("the fault does not say what was lost:\n%s", out)
	}
}

// A rotation that has quietly stopped shows up as the oldest thing still
// sitting there rather than as an error, since nothing has gone wrong yet.
func TestTheOldestThingStillOnTheBoxIsNamed(t *testing.T) {
	path := rotationLog(t, map[string][]string{
		"part-00003.warc.gz": {"resident"},
	})
	out, _, code := exec(t, "clear", "read", "-files", path)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "The oldest thing still on the box is part-00003.warc.gz") {
		t.Errorf("the stuck file was not named:\n%s", out)
	}
	if !strings.Contains(out, "data/gao-crawl-2026-09") {
		t.Errorf("the per file table does not carry the store path:\n%s", out)
	}
}

func TestTheLedgerIsAvailableAsJSON(t *testing.T) {
	path := rotationLog(t, map[string][]string{
		"part-00004.warc.gz": {"resident", "pushed", "verified", "reclaimed"},
	})
	out, errOut, code := exec(t, "clear", "read", "-json", path)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got clearReadReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Sound || got.Files != 1 || got.Reclaimed != 1_000_000_000 || got.OnDisk != 0 {
		t.Errorf("a clean log read as %+v", got)
	}
}

func TestALogThatIsNotThereIsAFailure(t *testing.T) {
	_, errOut, code := exec(t, "clear", "read", filepath.Join(t.TempDir(), "nothing.jsonl"))
	if code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errOut)
	}
	if !strings.Contains(errOut, "gao clear:") {
		t.Errorf("the failure was not attributed: %s", errOut)
	}
}

func TestClearWithoutASubcommandPrintsUsage(t *testing.T) {
	_, errOut, code := exec(t, "clear")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "usage: gao clear") {
		t.Errorf("no usage printed: %s", errOut)
	}
}

func TestAnUnknownClearSubcommandSaysSo(t *testing.T) {
	_, errOut, code := exec(t, "clear", "clean")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
	if !strings.Contains(errOut, "no subcommand named clean") {
		t.Errorf("the unknown subcommand was not named: %s", errOut)
	}
}

func TestClearHelpGoesToStdout(t *testing.T) {
	out, _, code := exec(t, "clear", "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "usage: gao clear") {
		t.Errorf("help did not go to stdout: %s", out)
	}
}

func TestClearReadWantsExactlyOneLog(t *testing.T) {
	_, errOut, code := exec(t, "clear", "read")
	if code != 2 {
		t.Fatalf("exit %d, want 2: %s", code, errOut)
	}
}
