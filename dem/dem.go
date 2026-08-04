// Package dem counts, exactly, in the units gao publishes.
//
// Every headline number in this project is a token count, and a token count is
// only meaningful once the tokenizer is named. One gao token is one token under
// the Gemma-3 vocabulary, and this package is where that stops being a sentence
// in a design document and becomes a file with a digest.
//
// The counts are produced by counting. doc/units.go carries conversion constants
// and they are for estimates: they answer what a hundred gigabytes is roughly
// worth before anything has been fetched. Nothing in this package multiplies.
// The reason for the separation is that an estimate copied into a release note
// becomes a measurement in the reader's mind, and there is no way to take it
// back, so the two live in different packages and the counting one has no
// constants in it.
//
// Counting happens in the pass that already reads the bytes. The largest source
// is around 700 GB of text, so a design where ingestion writes documents and a
// later stage reads them back to count is a design that moves 700 GB twice. A
// [Tally] is attached to the ingest and the numbers come out of the run that was
// happening anyway.
//
// Tokenizing is the expensive part and it is opt-in for that reason. It runs at
// about 11 MB of text per second per core, which is faster than any source has
// so far arrived over the network and slower than a local disk, so whether it
// costs anything depends on what else the run is waiting for. Bytes, characters,
// and syllables are counted always because they are free.
package dem
