// Package listen decides whether a transcript is worth keeping without having a
// reference transcript to score it against.
//
// Nghe is to listen. At corpus scale nobody has the words that were actually
// said, so a word error rate is not available and the checks that are left are
// the ones that need no reference. That sounds like a weaker position than it
// is, because the failure that matters here is not a wrong word. It is a
// decoder that hits a stretch of silence, or music, or a tone it has no model
// for, and starts emitting the same sentence until the audio runs out.
//
// That failure is invisible everywhere downstream. The loop is fluent
// Vietnamese, so the language filter admits it. It sits inside one document, so
// nothing that looks for duplicate documents finds it. It has plausible
// punctuation and plausible diacritics. What it does is train a model to repeat
// itself, and the only place it can be caught is where the transcript is made,
// which is why this is a check and not a note in the extraction log.
//
// Three things are measured, and they fail differently. The longest run of one
// line repeated back to back, and the share of the lines that are distinct,
// which are the loop seen from two directions. The syllables a track carries
// against the seconds of speech it claims to carry them in, since a transcript
// running at one syllable a second dropped most of the audio and one running at
// twelve invented some. Both are cheap, and both are wrong to skip, because a
// decoder that loops on one recording is a decoder setting rather than an
// unlucky file.
//
// Human authored subtitles and generated ones are kept apart rather than
// blended, and where a recording has both, the human track is the one admitted
// and the generated one is superseded rather than dropped. A superseded track
// is not a loss, which is why it is not counted as one.
package listen

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Human and Machine are the two ways a transcript comes to exist, and the whole
// of the preference item is that a set says which one it is looking at.
const (
	Human   = "human"
	Machine = "asr"
)

// MaxRepeat is the longest run of one line repeated back to back before the
// decoder is looping rather than the speaker.
const MaxRepeat = 3

// MinVariety is the share of a track's lines that have to be distinct. A talk
// with a refrain in it clears this comfortably. A decoder stuck on silence does
// not.
const MinVariety = 0.6

// MinRate and MaxRate bound syllables a second of speech. Vietnamese is
// syllable timed and lands around six in ordinary conversation, so a transcript
// outside these is not a transcript of the audio under it.
const (
	MinRate = 2.0
	MaxRate = 8.5
)

// MinSeconds is the shortest recording worth reading a decoder off.
const MinSeconds = 60.0

// MinSegments is the fewest timed lines before repetition means anything.
const MinSegments = 20

// MaxLost is the share of the hours that may fail these checks before the
// problem is the decoder rather than the audio. A few bad recordings are a
// corpus. A tenth of them is a setting.
const MaxLost = 0.10

// Card is the GPU memory the plan is written against, in gigabytes, since every
// decode on this fleet happens on one card.
const Card = 24.0

// A Track is one recording and the transcript that came off it.
type Track struct {
	// Track is the recording, and it is the same string for the human authored
	// subtitles and for the machine transcript of the same audio.
	Track string `json:"track"`

	// Source is human or asr.
	Source string `json:"source"`

	// Model is the engine that produced a machine transcript, and empty on a
	// human authored one.
	Model string `json:"model"`

	// Box is where the decode ran, because there is one GPU on this fleet.
	Box string `json:"box"`

	// Seconds is the length of the recording and Spoken is how much of it the
	// timed lines cover, which is the part a rate can be read off.
	Seconds float64 `json:"seconds"`
	Spoken  float64 `json:"spoken"`

	// Segments is the timed lines, Distinct is how many of them are different
	// from each other, and Repeats is the longest run of one line back to back.
	Segments int `json:"segments"`
	Distinct int `json:"distinct"`
	Repeats  int `json:"repeats"`

	// Syllables is what the transcript holds, counted the way the rest of gao
	// counts them.
	Syllables int `json:"syllables"`

	// VRAM is the peak the decode needed, in gigabytes, so a published result
	// is one that reproduces on the same card.
	VRAM float64 `json:"vram"`
}

// Generated reports whether a model wrote this transcript.
func (t Track) Generated() bool { return t.Source == Machine }

// Authored reports whether a person wrote this transcript.
func (t Track) Authored() bool { return t.Source == Human }

// Hours is the recording in the unit a set of them is described in.
func (t Track) Hours() float64 { return t.Seconds / 3600 }

// Covered is the share of the recording the timed lines account for.
func (t Track) Covered() float64 {
	if t.Seconds <= 0 {
		return 0
	}
	return t.Spoken / t.Seconds
}

// Variety is the share of the lines that are distinct.
func (t Track) Variety() float64 {
	if t.Segments <= 0 {
		return 0
	}
	return float64(t.Distinct) / float64(t.Segments)
}

// Rate is syllables a second of speech, measured against the seconds the timed
// lines cover rather than against the length of the recording, since silence is
// not slow speech.
func (t Track) Rate() float64 {
	if t.Spoken <= 0 {
		return 0
	}
	return float64(t.Syllables) / t.Spoken
}

// Looped reports whether the transcript repeats itself the way a decoder does.
func (t Track) Looped() bool { return t.Repeats >= MaxRepeat || t.Variety() < MinVariety }

// Drifted reports whether the transcript carries too few or too many syllables
// for the speech it claims to transcribe.
func (t Track) Drifted() bool { return t.Rate() < MinRate || t.Rate() > MaxRate }

// Kept reports whether this transcript can go in the corpus.
func (t Track) Kept() bool { return !t.Looped() && !t.Drifted() }

// Margin is how much room the track has before the nearest of the three checks
// fails it, as a ratio where one is the gate. It exists to order the set worst
// first, which is the order the interesting tracks are in.
func (t Track) Margin() float64 {
	repeats := float64(MaxRepeat) / float64(t.Repeats+1)
	variety := t.Variety() / MinVariety
	rate := t.Rate() / MinRate
	if high := MaxRate / t.Rate(); t.Rate() > 0 && high < rate {
		rate = high
	}
	return min(repeats, min(variety, rate))
}

// Blocking is every reason this track is not a reading of a decoder.
func (t Track) Blocking() []string {
	var why []string
	if t.Track == "" {
		return []string{"a transcript with no recording on it is a body of text, and what this checks is whether it belongs to the audio it came from"}
	}
	if !t.Authored() && !t.Generated() {
		why = append(why, fmt.Sprintf(
			"%s does not say whether a person wrote it or a model did, and keeping both is only worth anything if they are told apart",
			t.Track))
	}
	if t.Generated() && t.Model == "" {
		why = append(why, fmt.Sprintf("%s was decoded by an engine nobody named, so the transcript is not one anybody can produce a second time", t.Track))
	}
	if t.Generated() && t.Box == "" {
		why = append(why, fmt.Sprintf("%s does not say which box decoded it, and there is one card on this fleet that can have", t.Track))
	}
	if t.Generated() && t.VRAM <= 0 {
		why = append(why, fmt.Sprintf(
			"%s records no peak VRAM, and a decode that does not say what it needed against %.0f GB is a result that reproduces by luck",
			t.Track, Card))
	}
	if t.VRAM > Card {
		why = append(why, fmt.Sprintf(
			"%s claims %s of peak VRAM against a card that holds %.0f GB, so what is recorded is not what ran",
			t.Track, memory(t.VRAM), Card))
	}
	if t.Authored() && (t.Model != "" || t.VRAM > 0) {
		why = append(why, fmt.Sprintf("%s is a human authored track with a decoder recorded against it, which is the one confusion this set exists to avoid", t.Track))
	}
	if t.Seconds < MinSeconds {
		why = append(why, fmt.Sprintf(
			"%s is %s long, under the %s a decoder needs to have a chance to come off the rails, so it is a sample rather than a reading",
			t.Track, length(t.Seconds), length(MinSeconds)))
	}
	if t.Segments < MinSegments {
		why = append(why, fmt.Sprintf(
			"%s carries %d timed lines, under %d, and repetition in a handful of lines is a speaker rather than a loop",
			t.Track, t.Segments, MinSegments))
	}
	if t.Spoken <= 0 {
		why = append(why, fmt.Sprintf("%s records no speech inside its lines, so there is nothing for the syllables to have been said in", t.Track))
	}
	if t.Spoken > t.Seconds && t.Seconds > 0 {
		why = append(why, fmt.Sprintf(
			"%s covers %s of speech in a %s recording, and a transcript cannot hold more of a recording than the recording holds",
			t.Track, length(t.Spoken), length(t.Seconds)))
	}
	if t.Distinct > t.Segments {
		why = append(why, fmt.Sprintf("%s counts %d distinct lines out of %d, which is not a count of anything", t.Track, t.Distinct, t.Segments))
	}
	if t.Syllables <= 0 {
		why = append(why, fmt.Sprintf("%s counts no syllables, and syllables against seconds of speech is the check that says whether the words match the audio", t.Track))
	}
	return why
}

// A Set is the tracks a speech artifact holds.
type Set struct {
	// Set is the artifact these tracks belong to, which is gao-voice.
	Set string `json:"set"`

	Tracks []Track `json:"tracks"`
}

// Superseded is every machine transcript of a recording that also has a human
// authored one. They are kept, since the audio and the alignment are still
// worth having, and they are not admitted, since a person already wrote the
// words.
func (s Set) Superseded() []Track {
	authored := map[string]bool{}
	for _, t := range s.Tracks {
		if t.Authored() {
			authored[t.Track] = true
		}
	}
	return s.filter(func(t Track) bool { return t.Generated() && authored[t.Track] })
}

// Considered is every track that stands on its own, which is the set the gate
// is read off.
func (s Set) Considered() []Track {
	beaten := map[string]bool{}
	for _, t := range s.Superseded() {
		beaten[t.Track+"\x00"+t.Source] = true
	}
	return s.filter(func(t Track) bool { return !beaten[t.Track+"\x00"+t.Source] })
}

// Admitted is what goes in the corpus.
func (s Set) Admitted() []Track { return keep(s.Considered(), Track.Kept) }

// Dropped is what does not.
func (s Set) Dropped() []Track {
	return keep(s.Considered(), func(t Track) bool { return !t.Kept() })
}

// Looping is every track the decoder repeated itself on.
func (s Set) Looping() []Track { return keep(s.Considered(), Track.Looped) }

// Drifting is every track whose words do not fit the speech under them.
func (s Set) Drifting() []Track { return keep(s.Considered(), Track.Drifted) }

// Ranked is the tracks worst first, since the ones near a gate are the ones
// worth reading and the clean ones all look alike.
func (s Set) Ranked() []Track {
	out := slices.Clone(s.Tracks)
	slices.SortStableFunc(out, func(a, b Track) int {
		switch {
		case a.Margin() < b.Margin():
			return -1
		case a.Margin() > b.Margin():
			return 1
		default:
			return strings.Compare(a.Track+a.Source, b.Track+b.Source)
		}
	})
	return out
}

// Worst is the track closest to a gate, and false when nothing was read.
func (s Set) Worst() (Track, bool) {
	if len(s.Tracks) == 0 {
		return Track{}, false
	}
	// Taken off the ranking rather than searched for again, so two tracks at
	// the same margin name the same one in the table and in the sentence.
	return s.Ranked()[0], true
}

// Nearest is the admitted track that came closest to a gate, and false when
// nothing was admitted. It is not the same as Worst, since the track nearest a
// gate among the ones that failed it is on the other side of it.
func (s Set) Nearest() (Track, bool) {
	in := s.Admitted()
	if len(in) == 0 {
		return Track{}, false
	}
	return in[0], true
}

// Hours is the audio the gate is read over.
func (s Set) Hours() float64 { return hours(s.Considered()) }

// Lost is the hours that failed the checks.
func (s Set) Lost() float64 { return hours(s.Dropped()) }

// Share is the hours lost against the hours considered.
func (s Set) Share() float64 {
	if s.Hours() <= 0 {
		return 0
	}
	return s.Lost() / s.Hours()
}

// Written is the hours a person wrote the words for, which is the half of the
// set that needs none of this.
func (s Set) Written() float64 {
	return hours(keep(s.Considered(), Track.Authored))
}

// Blocking is every reason this set does not measure a decoder.
func (s Set) Blocking() []string {
	if len(s.Tracks) == 0 {
		return []string{"no transcript was read, so what gao-voice holds is audio and an intention"}
	}
	var why []string
	if s.Set == "" {
		why = append(why, "the tracks do not say which set they belong to, and an artifact is what gets published rather than what gets decoded")
	}
	seen := map[string]bool{}
	for _, t := range s.Tracks {
		key := t.Track + "\x00" + t.Source
		if seen[key] {
			why = append(why, fmt.Sprintf("%s appears twice from the same source, and two transcripts of one recording are not two recordings", t.Track))
		}
		seen[key] = true
		why = append(why, t.Blocking()...)
	}
	return why
}

// Settled reports whether this is a reading rather than a listing.
func (s Set) Settled() bool { return len(s.Blocking()) == 0 }

// Holds reports whether the decoder is producing transcripts a corpus can take.
func (s Set) Holds() bool { return s.Settled() && s.Share() <= MaxLost }

// Verdict is the set in one sentence.
func (s Set) Verdict() string {
	w, ok := s.Worst()
	if !ok {
		return "no transcript was read, so what is published about the speech path is the pipeline rather than what came out of it"
	}
	if why := s.Blocking(); len(why) > 0 {
		return why[0]
	}
	if s.Share() > MaxLost {
		if loops := s.Looping(); len(loops) > 0 {
			t := loops[0]
			return fmt.Sprintf(
				"%s of the hours came back unusable against the %s a corpus absorbs, and %s repeats one line %d times in a row with %s of its lines distinct, which is a decoder setting rather than a bad recording",
				share(s.Share()), share(MaxLost), t.Track, t.Repeats, share(t.Variety()))
		}
		t := s.Drifting()[0]
		return fmt.Sprintf(
			"%s of the hours came back unusable against the %s a corpus absorbs, and %s carries %s of speech against the %.1f to %.1f a person talks at, so the words are not the words in that audio",
			share(s.Share()), share(MaxLost), t.Track, rate(t.Rate()), MinRate, MaxRate)
	}
	if near, ok := s.Nearest(); ok {
		w = near
	}
	return fmt.Sprintf(
		"%s holds %s of transcript a corpus can take, %s of it written by a person, and the nearest any admitted track came to a gate was %s keeping %s of its lines distinct at %s of speech",
		s.Set, length(3600*(s.Hours()-s.Lost())), length(3600*s.Written()), w.Track, share(w.Variety()), rate(w.Rate()))
}

func (s Set) filter(want func(Track) bool) []Track { return keep(s.Ranked(), want) }

func keep(in []Track, want func(Track) bool) []Track {
	var out []Track
	for _, t := range in {
		if want(t) {
			out = append(out, t)
		}
	}
	return out
}

func hours(in []Track) float64 {
	var total float64
	for _, t := range in {
		total += t.Hours()
	}
	return total
}

// ReadSet loads a set from a file of one JSON track per line.
func ReadSet(name, path string) (Set, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Set{}, fmt.Errorf("nghe: %w", err)
	}
	s := Set{Set: name}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var t Track
		if err := dec.Decode(&t); err != nil {
			return Set{}, fmt.Errorf("nghe: %s line %d: %w", path, i+1, err)
		}
		s.Tracks = append(s.Tracks, t)
	}
	if len(s.Tracks) == 0 {
		return Set{}, fmt.Errorf("nghe: %s holds no tracks", path)
	}
	return s, nil
}

// length renders a duration the way somebody describes a recording, which is in
// hours above an hour and in minutes below one.
func length(f float64) string {
	switch {
	case f >= 3600:
		return fmt.Sprintf("%.1fh", f/3600)
	case f >= 60:
		return fmt.Sprintf("%.0fm", f/60)
	default:
		return fmt.Sprintf("%.0fs", f)
	}
}

func share(f float64) string { return fmt.Sprintf("%.1f%%", 100*f) }

func rate(f float64) string { return fmt.Sprintf("%.1f syllables a second", f) }

func memory(f float64) string { return fmt.Sprintf("%.1f GB", f) }
