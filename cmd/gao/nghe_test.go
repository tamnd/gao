package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trackLine writes one track the way the extraction pass records it, an hour of
// audio decoded on the box with the card in it.
func trackLine(name string, segments, distinct, repeats, syllables int) string {
	return fmt.Sprintf(
		`{"track":%q,"source":"asr","model":"whisper-large-v3","box":"gamingpc","seconds":3600,"spoken":3000,`+
			`"segments":%d,"distinct":%d,"repeats":%d,"syllables":%d,"vram":9.4}`,
		name, segments, distinct, repeats, syllables)
}

// ngheSet writes a set the decoder got right unless a test says otherwise.
func ngheSet(t *testing.T, lines ...string) string {
	t.Helper()
	if len(lines) == 0 {
		lines = []string{
			trackLine("vtv-thoi-su-1", 800, 760, 1, 17000),
			trackLine("vov-phong-van-2", 640, 620, 1, 15400),
			trackLine("hocmai-bai-giang-3", 900, 880, 2, 18200),
		}
	}
	path := filepath.Join(t.TempDir(), "tracks.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheTranscriptReportLeadsWithTheTrackNearestAGate(t *testing.T) {
	out, errOut, code := exec(t, "nghe", ngheSet(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	for _, want := range []string{
		"vtv-thoi-su-1", "gamingpc", "gao-voice",
		"gao-voice holds 3.0h of transcript a corpus can take",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not carry %q:\n%s", want, out)
		}
	}
	rows := strings.Split(out, "\n")
	if !strings.HasPrefix(rows[1], "hocmai-bai-giang-3") {
		t.Errorf("the table does not lead with the track nearest a gate:\n%s", out)
	}
}

// The failure the command exists for, and the one every filter downstream of it
// would pass.
func TestADecoderLoopingOnSilenceExitsTwo(t *testing.T) {
	out, _, code := exec(t, "nghe", ngheSet(t,
		trackLine("vtv-thoi-su-1", 800, 41, 214, 17000),
		trackLine("vov-phong-van-2", 640, 620, 1, 15400),
	))
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "which is a decoder setting rather than a bad recording") {
		t.Errorf("the report does not say what a loop is:\n%s", out)
	}
	if !strings.Contains(out, "loop") {
		t.Errorf("the table does not mark the looping track:\n%s", out)
	}
}

// Words that do not fit the speech under them, in both directions.
func TestATranscriptThatDoesNotFitItsAudioExitsTwo(t *testing.T) {
	out, _, code := exec(t, "nghe", ngheSet(t,
		trackLine("vtv-thoi-su-1", 800, 760, 1, 2100),
		trackLine("vov-phong-van-2", 640, 620, 1, 15400),
	))
	if code != 2 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "so the words are not the words in that audio") {
		t.Errorf("the report does not say what a drifted rate is:\n%s", out)
	}
}

// One GPU on the fleet, so a decode is a claim about a machine that has it.
func TestADecodeOnABoxWithNoCardInItExitsOne(t *testing.T) {
	nowhere := strings.Replace(trackLine("vtv-thoi-su-1", 800, 760, 1, 17000), `"box":"gamingpc"`, `"box":"server9"`, 1)
	out, _, code := exec(t, "nghe", ngheSet(t, nowhere, trackLine("vov-phong-van-2", 640, 620, 1, 15400)))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "which is not a box on this fleet") {
		t.Errorf("the report accepts a machine nobody has:\n%s", out)
	}

	cpu := strings.Replace(trackLine("vtv-thoi-su-1", 800, 760, 1, 17000), `"box":"gamingpc"`, `"box":"server1"`, 1)
	out, _, code = exec(t, "nghe", ngheSet(t, cpu, trackLine("vov-phong-van-2", 640, 620, 1, 15400)))
	if code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if !strings.Contains(out, "which has no card in it") {
		t.Errorf("the report accepts a VRAM figure off a box with no GPU:\n%s", out)
	}

	big := strings.Replace(trackLine("vtv-thoi-su-1", 800, 760, 1, 17000), `"vram":9.4`, `"vram":23.6`, 1)
	out, _, code = exec(t, "nghe", ngheSet(t, big, trackLine("vov-phong-van-2", 640, 620, 1, 15400)))
	if code != 0 {
		t.Fatalf("a decode that fits the card exited %d: %s", code, out)
	}
}

func TestTheTranscriptSetIsAlsoMachineReadable(t *testing.T) {
	out, errOut, code := exec(t, "nghe", "-json", ngheSet(t))
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, out, errOut)
	}
	var got struct {
		Set      string  `json:"set"`
		Tracks   int     `json:"tracks"`
		Hours    float64 `json:"hours"`
		Written  float64 `json:"written"`
		Lost     float64 `json:"lost"`
		Share    float64 `json:"share"`
		Admitted int     `json:"admitted"`
		Looping  int     `json:"looping"`
		Worst    string  `json:"worst"`
		Holds    bool    `json:"holds"`
		Readings []struct {
			Track   string  `json:"track"`
			Variety float64 `json:"variety"`
			Rate    float64 `json:"rate"`
			Kept    bool    `json:"kept"`
		} `json:"readings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Set != "gao-voice" || got.Tracks != 3 || !got.Holds {
		t.Errorf("the set came back as %+v", got)
	}
	if got.Hours != 3 || got.Written != 0 || got.Lost != 0 || got.Share != 0 {
		t.Errorf("hours %.1f, written %.1f, lost %.1f, share %.3f", got.Hours, got.Written, got.Lost, got.Share)
	}
	if got.Admitted != 3 || got.Looping != 0 || got.Worst != "hocmai-bai-giang-3" {
		t.Errorf("admitted %d, looping %d, worst %s", got.Admitted, got.Looping, got.Worst)
	}
	first := got.Readings[0]
	if !first.Kept || first.Rate < 6.0 || first.Rate > 6.2 {
		t.Errorf("the first reading came back as %+v", first)
	}
}

func TestNgheRefusesWhatItCannotRead(t *testing.T) {
	if _, _, code := exec(t, "nghe"); code != 2 {
		t.Errorf("no argument exited %d", code)
	}
	if _, _, code := exec(t, "nghe", "a.jsonl", "b.jsonl"); code != 2 {
		t.Errorf("two arguments exited %d", code)
	}
	if _, _, code := exec(t, "nghe", filepath.Join(t.TempDir(), "missing.jsonl")); code != 1 {
		t.Errorf("a file that is not there exited %d", code)
	}
}
