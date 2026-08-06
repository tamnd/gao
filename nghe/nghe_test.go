package nghe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decoded is an hour of audio a model transcribed on the one box that can
// transcribe anything, at a rate a person talks at.
func decoded(name string) Track {
	return Track{
		Track: name, Source: Machine, Model: "whisper-large-v3", Box: "gamingpc",
		Seconds: 3600, Spoken: 3000,
		Segments: 800, Distinct: 760, Repeats: 1,
		Syllables: 17000, VRAM: 9.4,
	}
}

// written is the same recording with subtitles a person typed, which needs no
// engine and no card.
func written(name string) Track {
	t := decoded(name)
	t.Source, t.Model, t.Box, t.VRAM = Human, "", "", 0
	return t
}

func set(tracks ...Track) Set {
	if len(tracks) == 0 {
		tracks = []Track{decoded("vtv-thoi-su-1"), decoded("vov-phong-van-2"), written("hocmai-bai-giang-3")}
	}
	return Set{Set: "gao-voice", Tracks: tracks}
}

func refuses(t *testing.T, s Set, want string) {
	t.Helper()
	for _, why := range s.Blocking() {
		if strings.Contains(why, want) {
			return
		}
	}
	t.Errorf("nothing blocking mentions %q, got:\n  %s", want, strings.Join(s.Blocking(), "\n  "))
}

func TestATranscriptIsJudgedWithoutAReferenceToJudgeItAgainst(t *testing.T) {
	s := set()
	if !s.Settled() {
		t.Fatalf("a clean set was refused: %v", s.Blocking())
	}
	if !s.Holds() {
		t.Fatalf("a set with nothing wrong in it did not hold: %s", s.Verdict())
	}
	if got := len(s.Admitted()); got != 3 {
		t.Errorf("%d tracks were admitted out of 3", got)
	}
	if got := s.Written(); got != 1 {
		t.Errorf("%.2f hours came back written by a person", got)
	}
	for _, want := range []string{"gao-voice holds 3.0h of transcript", "1.0h of it written by a person"} {
		if !strings.Contains(s.Verdict(), want) {
			t.Errorf("the verdict does not carry %q: %s", want, s.Verdict())
		}
	}
}

// The failure the package exists for. It is fluent, it is Vietnamese, it is one
// document, and nothing downstream of here would catch it.
func TestADecoderLoopingOnSilenceIsCaughtWhereTheTranscriptIsMade(t *testing.T) {
	s := set()
	s.Tracks[0].Segments = 800
	s.Tracks[0].Distinct = 41
	s.Tracks[0].Repeats = 214
	if s.Holds() {
		t.Fatal("a track repeating one line 214 times in a row held")
	}
	if got := len(s.Looping()); got != 1 {
		t.Errorf("%d tracks came back looping", got)
	}
	if !strings.Contains(s.Verdict(), "which is a decoder setting rather than a bad recording") {
		t.Errorf("the verdict does not say what a loop is: %s", s.Verdict())
	}
	// The short run of one line is the other half, and it fails on its own.
	only := set(decoded("vtv-thoi-su-1"))
	only.Tracks[0].Repeats = MaxRepeat
	if only.Holds() {
		t.Fatalf("three of one line in a row held: %s", only.Verdict())
	}
	// A refrain is not a loop, which is the whole reason there are two numbers.
	refrain := set()
	refrain.Tracks[0].Distinct = 500
	refrain.Tracks[0].Repeats = 2
	if !refrain.Holds() {
		t.Errorf("a talk with a refrain in it was thrown away: %s", refrain.Verdict())
	}
}

// Syllables against the speech they were said in, which catches the transcript
// that stopped early and the one that kept writing.
func TestWordsThatDoNotFitTheSpeechUnderThemAreNotATranscriptOfIt(t *testing.T) {
	dropped := set()
	dropped.Tracks[0].Syllables = 2100
	if dropped.Holds() {
		t.Fatal("a transcript at 0.7 syllables a second held")
	}
	if !strings.Contains(dropped.Verdict(), "so the words are not the words in that audio") {
		t.Errorf("the verdict does not say what a drifted rate is: %s", dropped.Verdict())
	}
	invented := set()
	invented.Tracks[0].Syllables = 39000
	if invented.Holds() {
		t.Fatal("a transcript at 13 syllables a second held")
	}
	if got := len(invented.Drifting()); got != 1 {
		t.Errorf("%d tracks came back drifting", got)
	}
	// The rate is read against the speech rather than against the recording, so
	// an hour with twenty minutes of talking in it is not slow speech.
	sparse := set()
	sparse.Tracks[0].Spoken = 1200
	sparse.Tracks[0].Syllables = 6800
	if !sparse.Holds() {
		t.Errorf("a recording that is mostly silence was read as a bad transcript: %s", sparse.Verdict())
	}
}

// A few bad recordings are a corpus. A tenth of the hours is a setting.
func TestOneBadRecordingIsNotABrokenDecoder(t *testing.T) {
	tracks := make([]Track, 0, 21)
	for i := range 20 {
		tracks = append(tracks, decoded(fmt.Sprintf("vov-phong-van-%d", i)))
	}
	bad := decoded("vtv-ban-tin-late")
	bad.Seconds, bad.Spoken = 300, 240
	bad.Segments, bad.Distinct, bad.Repeats = 60, 4, 44
	bad.Syllables = 1400
	s := set(append(tracks, bad)...)
	if !s.Holds() {
		t.Fatalf("five minutes of loop in twenty hours failed the set: %s", s.Verdict())
	}
	if got := len(s.Dropped()); got != 1 {
		t.Errorf("%d tracks were dropped", got)
	}
	if got := s.Share(); got > 0.01 {
		t.Errorf("five minutes in twenty hours came back as %.3f of the set", got)
	}
	// Worst first, because the clean tracks all look alike.
	if w, _ := s.Worst(); w.Track != "vtv-ban-tin-late" {
		t.Errorf("the ranking leads with %s", w.Track)
	}
	// The sentence about how close the set came is about the tracks that were
	// kept, since the track nearest the gate among the ones that failed it is on
	// the far side of the gate.
	if near, _ := s.Nearest(); near.Track == "vtv-ban-tin-late" {
		t.Error("the nearest admitted track is one that was dropped")
	}
	if strings.Contains(s.Verdict(), "vtv-ban-tin-late") {
		t.Errorf("the verdict quotes a track it threw away: %s", s.Verdict())
	}
}

// The preference item, which is only worth anything if the two are told apart.
func TestAHumanAuthoredTrackSupersedesTheMachineOneForTheSameRecording(t *testing.T) {
	s := set(decoded("vtv-thoi-su-1"), written("vtv-thoi-su-1"), decoded("vov-phong-van-2"))
	if !s.Settled() {
		t.Fatalf("a recording with both kinds of track was refused: %v", s.Blocking())
	}
	if got := len(s.Superseded()); got != 1 {
		t.Errorf("%d machine transcripts came back superseded", got)
	}
	if got := len(s.Considered()); got != 2 {
		t.Errorf("%d tracks were considered out of 3", got)
	}
	if got := s.Hours(); got != 2 {
		t.Errorf("the set came back as %.1f hours", got)
	}
	// A superseded track is not a loss, so a bad machine transcript of a
	// recording a person already wrote out does not count against the decoder.
	s.Tracks[0].Distinct, s.Tracks[0].Repeats = 12, 300
	if !s.Holds() || s.Lost() != 0 {
		t.Errorf("a superseded loop was counted as lost hours: %s", s.Verdict())
	}
}

func TestASetHasToBeAReadingOfADecoder(t *testing.T) {
	unnamed := set()
	unnamed.Tracks[0].Source = ""
	refuses(t, unnamed, "whether a person wrote it or a model did")

	noengine := set()
	noengine.Tracks[0].Model = ""
	refuses(t, noengine, "an engine nobody named")

	nobox := set()
	nobox.Tracks[0].Box = ""
	refuses(t, nobox, "there is one card on this fleet")

	novram := set()
	novram.Tracks[0].VRAM = 0
	refuses(t, novram, "a result that reproduces by luck")

	toomuch := set()
	toomuch.Tracks[0].VRAM = 31.5
	refuses(t, toomuch, "against a card that holds 24 GB")

	confused := set()
	confused.Tracks[2].Model = "whisper-large-v3"
	refuses(t, confused, "the one confusion this set exists to avoid")

	short := set()
	short.Tracks[0].Seconds = 40
	refuses(t, short, "a sample rather than a reading")

	thin := set()
	thin.Tracks[0].Segments = 11
	refuses(t, thin, "a speaker rather than a loop")

	silent := set()
	silent.Tracks[0].Spoken = 0
	refuses(t, silent, "nothing for the syllables to have been said in")

	over := set()
	over.Tracks[0].Spoken = 4000
	refuses(t, over, "cannot hold more of a recording than the recording holds")

	miscounted := set()
	miscounted.Tracks[0].Distinct = 900
	refuses(t, miscounted, "which is not a count of anything")

	wordless := set()
	wordless.Tracks[0].Syllables = 0
	refuses(t, wordless, "says whether the words match the audio")

	twice := set()
	twice.Tracks[1] = twice.Tracks[0]
	refuses(t, twice, "two transcripts of one recording are not two recordings")

	noset := set()
	noset.Set = ""
	refuses(t, noset, "an artifact is what gets published")

	notrack := Set{Set: "gao-voice", Tracks: []Track{{Source: Human}}}
	refuses(t, notrack, "a transcript with no recording on it")

	empty := Set{Set: "gao-voice"}
	if empty.Settled() || empty.Holds() {
		t.Error("a set with no tracks in it verified a decoder")
	}
	if _, ok := empty.Worst(); ok {
		t.Error("a set with no tracks in it has a worst one")
	}
	if !strings.Contains(empty.Verdict(), "the pipeline rather than what came out of it") {
		t.Errorf("the verdict on nothing reads %q", empty.Verdict())
	}
}

func TestASetIsReadFromWhatTheDecoderAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tracks.jsonl")
	body := `{"track":"vtv-thoi-su-1","source":"asr","model":"whisper-large-v3","box":"gamingpc","seconds":3600,"spoken":3000,"segments":800,"distinct":760,"repeats":1,"syllables":17000,"vram":9.4}

{"track":"hocmai-bai-giang-3","source":"human","model":"","box":"","seconds":3600,"spoken":3000,"segments":800,"distinct":760,"repeats":1,"syllables":17000,"vram":0}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSet("gao-voice", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tracks) != 2 || !s.Holds() {
		t.Fatalf("read %d tracks, holds %v: %s", len(s.Tracks), s.Holds(), s.Verdict())
	}
	if got := s.Tracks[0].Covered(); got < 0.83 || got > 0.84 {
		t.Errorf("fifty minutes of speech in an hour came back as %.3f of it", got)
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte(`{"track":"vtv-thoi-su-1","wer":0.11}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSet("gao-voice", bad); err == nil {
		t.Error("a line with a column nobody declared was read")
	}

	blank := filepath.Join(dir, "blank.jsonl")
	if err := os.WriteFile(blank, []byte("\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSet("gao-voice", blank); err == nil {
		t.Error("an empty file was read as a set")
	}
	if _, err := ReadSet("gao-voice", filepath.Join(dir, "missing.jsonl")); err == nil {
		t.Error("a set that is not there was read")
	}
}
