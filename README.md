# gao

The largest Vietnamese text corpus, the pipeline that builds it, and the models cooked from it.

`gao` (*gạo*, raw rice grain) assembles Vietnamese text from four sources: the public corpora on Hugging Face, a recovery pass over Common Crawl, a direct crawl of the Vietnamese web, and non-HTML modalities such as PDFs, scans, and speech. It cleans, deduplicates, and publishes the result as signed immutable snapshots with per-document provenance.

The naming runs the length of the pipeline, because rice processing and corpus processing turn out to be the same six verbs. Grain is harvested (*gặt*), dried (*phơi*), sifted (*sàng*), milled to separate grain from husk (*xay*, and the husk is *vỏ*), stored in a warehouse (*kho*), and cooked (*nấu*) into *cơm*.

## The claim

gao v1 targets **300 billion unique natural Vietnamese tokens**, deduplicated globally, every document carrying its provenance.

That is a record only if the incumbents are measured honestly, so the spec measures them. The largest existing public Vietnamese corpus is HPLT v3 `vie_Latn`, which we measure at 176B tokens. The largest corpus any published Vietnamese model has trained on is PhoGPT's, at 102B. CulturaX, the corpus most Vietnamese projects actually use, publishes 55.4B.

gao v1 at 300B is 1.7x HPLT v3, 2.9x PhoGPT, and 5.4x CulturaX. It is not 10x any of them, and we never claim it is. The deduplicated Vietnamese HTML web is a bounded object of roughly 205B tokens. Past that boundary the only moves left are a deeper crawl than Common Crawl performs, non-HTML modalities, and synthesis. gao does all three and labels which tokens came from which.

## Status

Early. The pipeline is being built milestone by milestone against a written spec, and every milestone carries a checklist issue with its gates and kill criteria. See the [milestones](https://github.com/tamnd/gao/milestones).

Nothing here is published yet. When a snapshot ships it will be immutable, content addressed, signed, and accompanied by a manifest that lists every input and every pipeline version.

## Install

```
go install github.com/tamnd/gao/cmd/gao@latest
```

Or build from source:

```
git clone https://github.com/tamnd/gao
cd gao
make build
```

The binary is static and cross compiles cleanly. `CGO_ENABLED=0` everywhere.

## Usage

One binary, subcommands named for the rice verbs.

```
gao gat pins                                # the ingest manifest: what we download, at which revision
gao gat drift                               # ask every host whether it still serves what we pinned
gao gat hf     -dir ingest/                 # harvest from Hugging Face, resuming where it left off
gao gat hf     -dir ingest/ -decode         # and put every record to the ingest contract as it streams
gao gat hf     -dir ingest/ -out parts/ -push  # and write parquet, push it, and free the disk as it goes
gao gat ledger -dir ingest/                 # what the harvest has finished so far
gao gat ledger -dir ingest/ -files          # every finished file, and how each one was read

gao dem model  -o tokenizer.model           # fetch the tokenizer that defines a gao token
gao dem gates  -tokenizer tokenizer.model parts/*.parquet  # and put it through the ten gates before trusting a count
gao gat hf     -dir ingest/ -tokenizer tokenizer.model  # and count tokens while harvesting
gao dem counts ingest/                      # what the harvest counted, per source
gao dem keys   glotcc-abc1234               # read a snapshot's document identities back out of the store
gao dem overlap keys/*.keys                 # what the sources have in common, counted rather than sampled
gao dem verify -level counts -counts ingest/  # check a published count against the store it came from
gao gat cc     --snapshots all              # recover Vietnamese from Common Crawl
gao gat crawl  --policy crawl.toml          # crawl the Vietnamese web directly
gao gat media  --from crawl                 # fetch PDFs, audio, video

gao phoi       doc.txt                      # dry: normalize a document and write it out
gao phoi -report ingest/*.txt               # what normalizing did, per document, with a total
gao phoi -report -total parts/*.parquet     # and over parts, where the total is the part anybody reads
gao sang       parts/*.parquet              # sift: which documents are Vietnamese prose, and why the rest are not
gao sang -min-syllables 40 parts/*.parquet  # and what a different length floor would keep
gao xay        parts/*.parquet              # mill: what the corpus holds more than one copy of
gao xay -curve parts/*.parquet              # and what every deduplication threshold would cost
gao xay -boiler parts/*.parquet             # and the furniture every page of a host carries
gao soi        page.txt reading.txt         # judge a machine's reading of a page against what it says
gao soi -matrix page.txt reading.txt        # and what each of the six tones was read as
gao che        doc.txt                      # cover: tag over the personal data in a document
gao che -level L2 -report parts/*.parquet   # and what a corpus holds, per kind, before covering it
gao nhat -benchmarks                        # pick out the grit: what gao is judged on, and it only grows
gao nhat -list benchmarks.json parts/*.parquet  # and which documents hold a benchmark's own test items
gao dau build -o vi-diacritic.jsonl parts/*.parquet  # the mark: build the diacritic restoration task set
gao dau baseline -items vi-diacritic.jsonl other/*.parquet  # the two numbers a model has to beat
gao dau grade -items vi-diacritic.jsonl answers.jsonl  # and score a model's answers against them

gao kho release --snapshot gao-v1.0         # store and publish
gao kho verify  snapshots/gao-v1.0          # check a snapshot against its manifest
gao kho reproduce snapshots/gao-v1.0        # rebuild its bytes and check they come out the same
gao kho remove  -from a -to b -snapshot b -key gao.key -reason takedown <docid>  # take a document back out
gao kho datasets                            # where processed data is written, and how to read it
gao kho push  part.parquet                  # send one file to the store, skipping what is already there
gao kho card  -dataset vietnamese-web-text  # generate a repo's dataset card from its snapshot manifest

gao box                                     # the fleet, and the disk budget it implies
gao luat                                    # the legal position and what it lets us publish
```

Run `gao help` for the full surface.

## Verifying a snapshot

Every snapshot carries a `manifest.toml` that lists its shards, the hash of each one, a merkle root over those hashes, and an ed25519 signature over the manifest values. Checking all of it is one command.

```
gao kho verify -key <the published gao key> snapshots/gao-v1.0
```

That checks four things: the manifest is internally consistent, the merkle root matches the shard hashes, the signature verifies against the key you named, and every shard file on disk hashes to the value recorded for it. A shard file present in the directory but absent from the manifest fails too, because a snapshot with an extra file in it is not the snapshot that was signed.

Pass `-quick` to check the manifest and the signature without rehashing several hundred gigabytes. It answers a different question and it is not enough to accept a download.

Without `-key` the signature is checked against the key embedded in the manifest, which proves the snapshot was signed by somebody rather than that it was signed by us. The published key goes in the release notes, and a verifier written against it is ten lines in any language: the key file is one line of hex and nothing else.

## Rebuilding a snapshot to check the bytes

The release has to be reproducible by somebody who does not trust us, and that claim is two claims that get quoted as one. The first is that the pipeline computes the same documents from the same inputs. The second is that writing those documents produces the same file. Only the second can be checked from a snapshot on its own, because the inputs are not in it, and it is the one that has to hold first: until it does, a stage that reruns to something different is indistinguishable from a compressor that does, and neither result means anything.

```
gao kho reproduce snapshots/gao-v1.0
```

That rebuilds every shard from the documents that shard holds and compares the result against the recorded hash. The documents are the same by construction, which is the point rather than a weakness. What can differ is the compressor version, a writer setting, or something in gao, so the report prints the versions of everything that decides bytes and the failure message says the corpus is intact, because otherwise the first response to a mismatch is somebody replacing a disk.

Nothing is written to disk. The rebuild is compared against the recording as it is produced, so a 512 MB shard costs a buffer rather than 512 MB of free space, which is what lets it run on `server1` inside 5 GB. When a shard does not come back, the report gives the byte offset where the two first differ and which frame of the segment that offset falls in, since the answer "somewhere in a 512 MB file" is not an answer. An offset past every frame is a different fault from one inside a frame: it means the documents compressed identically and the index written after them did not.

The snapshot is verified before any of this, because whether bytes rebuild to what a manifest says is not worth asking until something has established that the manifest is the one that was signed.

Stages get what checking they can have. A stage that is a function of the document can be checked against a snapshot without its inputs, by asking whether the document is a fixed point of it: normalizing an already normalized document, or classifying an already accepted one, has to change nothing. So `phoi` and `sang` run over every document as it streams past, and a snapshot cleaned by a different version than the manifest names comes back with a count and five document identities to go and open. It does not prove the stage ran on the right inputs. It catches the failure that otherwise surfaces months later as a model that trains badly for no visible reason.

Every other stage is listed as not checked, with the reason. An ingest stage cannot be re-run without the network and a deduplication stage cannot be re-run against one document at a time. A report that lists only what it checked reads as a report that checked everything, which is the specific way this kind of tool lies.

## Taking a document back out

Somebody will ask us to remove a document, and when they do it will be urgent. The mechanism is built for that day rather than for the day it was written.

A snapshot is immutable and its manifest is signed, so nothing is edited in place. A removal writes a new snapshot that names the old one as its parent and carries a tombstone for every document taken out.

```
gao kho remove -from snapshots/gao-v1.0 -to snapshots/gao-v1.0-r1 \
  -snapshot gao-v1.0-r1 -key gao.key -reason takedown -list request-118.txt
```

A tombstone keeps the document identity and nothing else. Not the text, not the URL, not the host. A tombstone that quotes what it removed has not removed it, and one that names the URL has published the fact that a particular page was the subject of a request, which is often the thing the person wanted taken down. The identity is kept because a later crawl that meets the same page has to recognize it and not fetch it again, and because somebody asking whether their document was removed deserves an answer they can check for themselves.

The shards that held none of the named documents are copied across byte for byte and keep the hashes the parent recorded for them, so a takedown that touches two shards out of 750 rewrites two files. That is the difference between answering in minutes and answering tomorrow.

Naming a document that is not in the snapshot fails the run and writes nothing, even when every other identity was found. A takedown answered with a signature and a report that quietly covers three documents out of four is the worst outcome available, because everybody involved reads it as done, and an identity that is not there is far more likely to be a mistyped hash than an empty request. Running the same removal twice is not an error: the second run finds the documents already tombstoned and says so, which is what makes this safe to put in a script that might get retried.

What happens to the parent afterwards is a publication decision rather than a storage one. Whether it is withdrawn, kept for the people who already have it, or left up, is a question for whoever answered the request, and this command will not answer it for them.

## What goes in

Six public corpora go in before gao crawls anything of its own. The ingest manifest is the list of exactly which files, at exactly which revision, and `gao gat pins` prints it.

| order | source | repo | files | download | license |
|---|---|---|---|---|---|
| 0 | HPLT v3 `vie_Latn` | `data.hplt-project.org/three/sorted` | 12 | 234.5 GB | CC0 |
| 1 | FinePDFs `vie_Latn` | `HuggingFaceFW/finepdfs` | 3 | 13.0 GB | ODC-By |
| 2 | FineWeb2 `vie_Latn` | `HuggingFaceFW/fineweb-2` | 30 | 130.1 GB | ODC-By |
| 3 | CulturaX `vi` | `uonlp/CulturaX` (gated) | 50 | 80.1 GB | inherits mC4 and OSCAR |
| 4 | MADLAD-400 `vi` | `allenai/MADLAD-400` | 32 | 95.3 GB, dropped | ODC-By |
| 5 | GlotCC-V1 `vie-Latn` | `cis-lmu/GlotCC-V1` | 27 | 55.9 GB | CC0 |

HPLT v3 ingests first and alone because it is the spine, and every later source dedups against a store that already holds it, which is what makes the retention numbers reproducible rather than dependent on what happened to arrive first.

Every Hub source is pinned to a commit SHA and never to a branch, because a corpus pinned to a moving target cannot be rebuilt from its own manifest. HPLT is the awkward one and it is also the largest: it is not hosted on the Hub, so there is no commit to pin, and what it publishes instead is a per language map file listing the shards. The manifest pins the sha256 of that map, which fixes the shard list, and records each shard's size from a HEAD.

`gao gat drift` asks every host what it serves now and reports the ones that have moved. It never rewrites the manifest. Re-pinning is a commit somebody makes deliberately, with the new file lists and byte counts read at the same time, because a manifest that re-pins itself silently changes what a released corpus was built from.

Reading the file lists off the hosts rather than copying them from the plan corrected the plan three times. GlotCC's Vietnamese partition was described as small and is 55.9 GB. The whole download was estimated at roughly 490 GB and is 608.9 GB, of which 513.6 GB is fetched and 95.3 GB is pinned and dropped for the reason below. CulturaX is gated, which nothing had recorded, and a gated repo does not hand its file digests to an unauthenticated caller, so that source pins byte counts and fills in digests when the grant lands.

One number sets the shape of the ingest. The largest pinned file is a 26.6 GB HPLT shard and `server1`'s entire peak disk budget is 4.1 GB, so ingestion decompresses in flight and writes gao shards as it goes rather than downloading a file and then reading it. Streaming is not an optimization here, it is the only thing that fits.

## Getting it in

`gao gat hf` fetches what the manifest pins. Nothing lands on disk except the ledger: a file is streamed through whatever consumes it and the bytes are never all in one place at once, which is what the 26.6 GB against 4.1 GB arithmetic above forces.

A transfer that size will be dropped. When it is, the fetch reconnects at the byte it stopped at with a range request and carries on, and the hash rolls forward across the reconnect because nothing is read twice. A host that answers a range request by starting the file over is a failure rather than a slow path, because taking it would mean hashing the first bytes twice and reporting a file larger than the one that exists.

Progress is the ledger, one JSON line per finished file, synced as it is written. An interrupted run is resumed by running the same command again: files already recorded at their pinned revision are skipped. An entry names the revision it was fetched at, so re-pinning a source invalidates its entries rather than letting a restart mix two revisions into one corpus. `gao gat ledger` reads it without taking it over, so it is safe to run against a box that is fetching.

One ingest at a time in one directory, enforced by a lock file. Two of them do not corrupt the ledger, because it is append only and keyed by source, revision and path, and it dedupes on read. What they do instead is harder to find: both build the same plan, both fetch the same shard, and the file count still looks right while the bytes moved and the document totals are counted twice. The document store does not survive it at all, since two writers appending to the same segment interleave and nothing can read past the first collision. The lock names the box, the process and the time, so the refusal says who is holding the directory rather than that something is. A run killed outright leaves its lock behind, and the next run on the same box breaks it once it has established that the process is gone, which is a question with an answer where a timeout is a guess: a stalled 26.6 GB download and a dead one look the same from outside. A lock written by another box is never broken, because a process ID from another machine means nothing on this one. `gao gat hf -plan` takes no lock at all, so the plan stays readable while an ingest is running.

Every file is checked at the end against the byte count in the manifest, and against the pinned digest where the host publishes one. Where it does not, and HPLT publishes none while the Hub withholds them for gated repos, the fetch computes a digest and records it, so the second fetch of a file has something to compare against even though the first did not.

The counts are written the same way the ledger is, which took a fleet run to notice. A decoding run tallies documents, bytes, characters, syllables and tokens as it goes and writes them to `counts.json` beside the ledger, and the first version wrote that file once, when the run ended. A run over one of these sources takes days. So for days the directory held the previous run's counts, naming a source the box was no longer fetching, and nothing about the file said so: it parses, it has a box on it, and `gao dem counts` would print it without complaint. The counts are now written before the first byte is fetched and rewritten after every finished file, and a report written mid run says it is one. `gao dem counts` names the boxes that had not finished, because a prefix of a source and a source total are the same shape.

`-dir` has no default. A command that starts a 513.6 GB download into whichever directory it was run from is a command that does it once by accident.

## What a record becomes

Without `-decode` the bytes are counted and thrown away, which is the check that a source can be fetched at all. With it, every upstream record is mapped onto a gao document and put to the ingest contract.

A decoder is a mapping and not a parser. It says which upstream field is the URL, which one is the fetch time, and which of the producer's own measurements are worth keeping, and then it hands over a document for the contract to rule on. It decides nothing about quality. Two things happen at decode time anyway, and both because they are part of a document's identity rather than its quality. The text goes into NFC, since `doc_id` is a hash of it and a hash of two encodings of the same string is two documents. And the diacritic verdict is computed, since Vietnamese typed without tone marks is real Vietnamese and a different distribution, and a mixture that wants one and not the other cannot separate them after the fact. It is judged per line rather than per document, because the case worth catching is a page whose article carries tone marks and whose comments do not, and over a whole document that reads as a weak present.

Documents that fail the contract go to `-rejects` with the reason and the specific value that failed, so a rejection rate is a number somebody can look up rather than a thing somebody suspects. The store keeps a sample of the text and every measurement for all of it, which is what retuning a threshold actually needs. A file that produces documents and admits none of them stops the run rather than being reported: it means either the mapping is wrong or the source cannot satisfy the contract, and either way the next sixty files will do the same thing.

That has already found something, and it cost a source. MADLAD-400's clean split is a JSON object with one field in it, `text`, and there is no URL, no timestamp, and no media type, because Allen AI did not publish them. Every record decodes and every record is rejected for provenance it does not have. Four hundred records read from each of three shards spread across the partition, `vi_clean_0000`, `vi_clean_0011` and `vi_clean_0031`, carry that single key in all twelve hundred, so this is the shape of the split and not one bad file. Design rule 3 settles it: a document that cannot carry provenance is dropped rather than admitted with nulls, and a source where that holds for every document is dropped the same way. So MADLAD-400 is marked dropped in the manifest, which takes 95.3 GB and 32 files out of the download and leaves the pinned revision, the file list, the byte counts and the digests where they are, next to the reason. Deleting the entry would leave the next reader asking why a dataset every Vietnamese corpus cites is absent, and the answer would be in a commit message nobody reads. Re-admitting it takes either Allen AI publishing the provenance or gao changing a design rule.

Five sources have a decoder today. The sixth is CulturaX, which is gated and whose terms have not been granted, so nobody has read a byte of it. Each of the five was written against the real file, and one written from a dataset card alone would be a guess with a version number on it. MADLAD-400's is among them and is what found the gap that dropped it, which is the argument for writing them that way. `gao gat hf -decode` refuses a source it cannot decode before it opens the ledger, and refuses a dropped one on the same terms, because finding either out two hundred gigabytes into a download is not finding it out.

## Reading Parquet without downloading it

Four of the six ship Parquet, three of them have mappings, and Parquet keeps its schema and its row group index in a footer at the end of the file. A reader has to know where the end is before it can read the beginning, so the format cannot be decoded from a stream that only goes forwards, and the files run 1.6 to 4.8 GB against a box that peaks at 4.1 GB for everything it is doing at once. Downloading one to read it is not available and neither is buffering it.

What is left is to read the parts that are wanted, over the network, by range request. The reader fetches in 4 MB windows rather than in whatever size it was asked for, because a Parquet reader asks for a page header, then a page, then the next page header, and one request per ask is tens of thousands of round trips for one file. It keeps 24 windows rather than one, because a row group is read one column at a time with as many live read positions as the schema is wide, and a single cached window would be evicted by every column in turn and hit nothing. GlotCC settles the number: its 2.1 GB file is one row group of half a million rows across thirteen columns.

The cost of reading this way is the digest. A streamed file is hashed as it goes and checked at the end against what was pinned. A file read in pieces never has all of its bytes in one place, so there is nothing to hash, and the ledger records that rather than papering over it: the entry says the file was read at random, carries no digest, and records how many bytes actually crossed the wire and how many requests it took. `gao gat ledger -files` prints "read in pieces" in the digest column, because an empty cell reads as a bug. Without `-decode` those sources are streamed and verified like the others, since the footer stops a decoder from reading forwards and does not stop a hash.

A Parquet row also has no bytes of its own. It is a slice through as many column chunks as the schema is wide, sitting in separate pages that may not be adjacent in the file, so there is no equivalent of the JSON line whose hash becomes `raw_id`. What gao hashes instead is the row's fields as it read them, in schema order. Two rows identical in every column gao reads hash the same, which is what that identity is for.

Four mapping decisions in this layer are worth stating out loud, because each one is gao asserting something the producer did not.

None of the Parquet sources publishes a media type, and the contract requires one. All of them carry the URL, the fetch date, and the WARC record the document came from, and none of them says what was served at that URL. So it is asserted per source rather than globally: `text/html` for FineWeb2 and GlotCC because they are text extracted from the HTML pages of Common Crawl WARCs, and `application/pdf` for FinePDFs because it is text extracted from PDFs found in the same crawls, which is the whole reason it exists as a separate dataset. Neither is an inference about a particular document, and the extractor column records which mapping made the call at which version.

FineWeb2's language scores come back at 1.0000098943710327, and the contract requires a probability in (0, 1]. Every row in a 130.1 GB source would be rejected on the eighth decimal place. That is a float landing slightly above one rather than a claim to be more certain than certain, so it is clamped to 1.

FinePDFs has its own `extractor` column, holding `docling` or `rolmOCR`, and gao has a column of that name meaning which mapping built the document. The upstream one goes to `upstream_fields` as `pdf_extractor`. Its published `token_count` gets the same treatment for the same reason: it is a count by a different tokenizer, and writing it into `n_tokens` would make a mixture built on token budgets wrong by however much the two disagree.

FinePDFs also writes its dates three ways in one file. The first shard has rows ending in `Z`, rows ending in `+00:00`, and at row 416 a row ending in nothing at all. The zoneless ones are read as UTC. The alternative is failing an unknown share of a 13.0 GB source over a formatting difference rather than over anything about the document, and this particular field is the WARC fetch date, WARC records carry UTC, and every zoned timestamp beside it in the same file is a zero offset. That is a reading of one field in one source and the code says so, because a general rule that a naive timestamp means UTC is how a corpus picks up an hour of drift that nobody can find afterwards.

## What a token is

Every headline in this project is a token count, and a token count means nothing until the tokenizer is named. One gao token is one token under the Gemma-3 vocabulary of 262144 pieces. Not a word, not a syllable, and not a token under whatever was installed on the box that ran the count.

That vocabulary lives in a 4.7 MB file, and the file is gated at Google's own repositories: reaching it there means accepting a license in a browser, which a program cannot do. So gao fetches it from a mirror and pins it by sha256. The digest is what makes a mirror acceptable, because it does not matter who serves the bytes if the bytes are known, and four separately uploaded repositories across the Gemma-3 family carry this one identically.

The pin is not ceremony. Ask a gated repository for a file without credentials and the refusal arrives as a body: 129 bytes of English prose written into the file where a protobuf should be. The download succeeded, the file exists, and nothing about it looks wrong to a program that checks only whether the write returned an error. Two of the four mirrors tried while writing this produced exactly that, and the test for it is the error page verbatim.

```
gao dem model -o tokenizer.model
```

Counting happens during ingestion rather than after it. The largest source is around 700 GB of text, so a design where ingestion writes documents and a later stage reads them back to count is a design that moves 700 GB twice. Bytes, characters, and syllables are counted on every decoding run because they are free. Tokens are behind `-tokenizer`, because tokenizing runs at about 11 MB of text per second per core, which is faster than any source has arrived over the network so far and slow enough to matter the first time one does not.

The four units are not interchangeable, and the bytes column is the one most often quoted wrong. Bytes here means UTF-8 bytes of extracted text: not the size of the file the text arrived in, not the compressed size, and not the Parquet size. Those are three to ten times apart from each other, and a corpus that quotes whichever was to hand has a size nobody can check. The ingest ledger records transfer sizes and `counts.json` records text sizes, in different files, because they answer different questions.

Two counts produced by different tokenizers are never added up. It is an error rather than a warning: two tokenizers disagree on Vietnamese by something like a third, so their sum is not slightly wrong, it corresponds to no tokenizer at all, and it would be quoted as a corpus size.

The first counted run puts a number on the estimate this project has been quoting, and it is not the estimated number. One GlotCC shard, 500000 documents, 3228869043 characters, 983022920 tokens: **3.28 characters per token**, where `doc/units.go` predicts 3.0 and the plan that wrote it allowed plus or minus 0.15. Tokens per syllable came out at 1.45 against a predicted 1.51 and bytes per character at 1.30 against 1.32, both close enough to leave alone. The character figure is not close, and it runs the same way every time: it means Vietnamese costs fewer tokens than the estimate assumed, so every token headline derived from a character count is about 8 percent high. That is one source of six and one shard of it, so it does not settle the corpus figure and the constants stay where they are, but it is the direction to expect from the rest.

The conversion constants in `doc/units.go` are for estimates and nothing in `dem` multiplies. They answer what a hundred gigabytes is roughly worth before anything has been fetched. They live in a different package from the counting on purpose, because an estimate that reaches a release note becomes a measurement in the reader's mind and there is no way to take it back.

## What a tokenizer has to pass first

The tokenizer is a measuring instrument, every headline here is quoted in its units, and an instrument gets checked before its readings do. There are ten checks. They are cheap, they are absolute, and `gao dem gates` runs all of them over a sample of the corpus and reports the fertility it measured on the same text.

The first three are one question asked of text that fails in different ways: does `decode(encode(x))` give back `x`, on the corpus, on the corpus with its marks taken off, and on the documents that mix Vietnamese with foreign words and with code. The threshold is 100.000% rather than 99.99%, and the extra digits are the point. One failure in ten thousand documents is fifty thousand corrupted documents at the size this is aiming for, and they do not spread evenly. They collect in the old orthography, the minority language quotations and the mathematical notation, which is text that is hard to notice missing and expensive to have got in the first place.

The fifth gate is the one this project added, and it is how the syllable question got settled. gao lets a token span a syllable boundary, so `Việt Nam` may be one token. Forbidding that would cap the useful vocabulary at the roughly 6200 syllables the language has, which wastes most of a 128k vocabulary and bars exactly the pairs that carry the most information. What gao forbids instead is splitting a letter from its marks. `ế` is one unit or part of a larger one, and never `e` followed by a fragment. A tokenizer that can split them produces a model that can emit the letter and then fail to emit the mark, which is tone loss arriving at generation time instead of at ingest, and the constraint makes it unrepresentable rather than rare.

A mark comes off in two ways and the suite counts both. A boundary can land inside a character, which is what byte fallback does, and a check on UTF-8 validity finds it. A boundary can also land between two characters and still be wrong, when the character after it is a combining mark belonging to the letter before it, and no validity check will ever see that one. Both are counted in boundaries rather than in documents, because a page holds thousands of boundaries and calling a bad page one failure hides how bad it is.

A gate that found nothing in the sample to run on is reported as not run, and a tokenizer is eligible only when all nine measured gates ran and passed. That distinction is the failure this suite is most likely to have in practice: point it at a thousand clean documents, three gates find nothing that applies, and a run that called that a pass prints ten green lines and means seven of them. The NFC gate says the same thing about itself in its own way, since a corpus that is already NFC compares every document against itself. The report gives the share of the sample that was not NFC as given, and when that share is zero it says so in as many words.

The last gate is an audit rather than a threshold and it stays that way. It walks the vocabulary and prints the pieces made of characters this project strips: replacement characters, private use, invisible formatting, anything that is not NFC. A piece like that is a fact about the corpus the tokenizer was trained on rather than about the text it will see here, and one of them is a hint while a thousand is a different tokenizer. No threshold decides that, a person does.

```
gao dem gates -tokenizer tokenizer.model parts/*.parquet
gao dem gates -tokenizer tokenizer.model -one-in 100 parts/*.parquet  # and the same run over one document in a hundred
```

## Normalizing before anything reads a character

Two spellings of one word are two documents. `hoà` and `hòa` are the same word written under two conventions, they hash to different values, and a document that reaches the corpus from two sources under two conventions survives deduplication as two, trains as two, and takes two rows in the embedding table. That is why normalization runs before anything else reads a character, and why it is a correctness problem rather than tidying.

Composition comes first and it is not enough on its own. NFC reorders combining marks by canonical class, and the acute of `ế` and the circumflex of `ê` are both of class 230, so it will not reorder them: an `e` followed by an acute followed by a circumflex is a permanently different string from `ế` and no amount of normalizing makes it one. It renders close enough that nobody notices. So `phoi` moves a tone mark that was typed ahead of the letter's own mark back on top of it and composes after that, which is a rule about Vietnamese rather than about Unicode and lives with the rest of the Vietnamese.

Then tone mark placement, toward the convention that puts the mark on the first vowel of the pair: `hoà` becomes `hòa`, `khoẻ` becomes `khỏe`, `thuỷ` becomes `thủy`. It fires on the rhymes `oa`, `oe` and `uy` with no final consonant, and nowhere else. Not on `hoàn` or `nguyệt`, where the coda decides which vowel carries the mark. Not on `quý` or `quỳ`, because after `qu` the u belongs to the consonant and is not part of the rhyme at all. Not on `hoài` or `nguyễn`, whose nuclei are three letters. Both conventions are correct Vietnamese and choosing between them is arbitrary. Choosing neither is what costs.

The look-alike letters are a question about the word rather than about the character. Eth for `đ` and the caron for the breve are repaired wherever they appear, because neither is a letter of any language this corpus holds, and the eth is the most common wrong codepoint in Vietnamese text by a distance. The Cyrillic and Greek letters drawn like Latin ones are a different case: a Cyrillic o inside `công ty` is damage, and a Cyrillic o inside a Russian word is a Russian word. So those are folded only inside a run of letters that is otherwise Latin, and a Vietnamese page quoting a line of Russian comes back byte for byte. The version of that table which did not ask turned `Пример` into `Пpимep`, which is the failure it was written to prevent arriving through the other door.

Invisible characters come out and are counted. A zero width space inside a syllable is not a typo a reader corrects, it makes that syllable a different string from every other copy of it through tokenization and into the embedding table, and it arrives by the thousand from pages laid out for print. The bidirectional controls come out for a second reason: they reorder what is displayed without touching what is stored, so a line can read one way to a person and mean another to everything that consumes it, which is a known attack on source code and the same trick in prose. Control characters come out too, and their rate is the useful part. Text carries none of them, so a file above 0.1 percent was sniffed as text and is really a font or an archive.

Input method residue is flagged and never repaired. `dduwowngj` is somebody's Telex keystrokes that never went through the input method, and `d9u7o7ng2` is the same accident in VNI. Repairing either means guessing which word was meant, and a guess written into a corpus is indistinguishable from a fact afterwards, so `phoi` counts them and sends a document above 2 percent to the reject store with `residue` as the reason, which is also where a file above the control character limit goes, as `control`. The detector is built for precision rather than recall, because a false positive throws away a good document over a word that merely looks like keystrokes, and English words ending in a Telex tone key are everywhere. What it misses is written down in a test rather than left to be rediscovered: `veef`, `lamf`, `minhf` and `khoongr` all go through.

The `i` and `y` variation is deliberately not normalized. `Mĩ` and `Mỹ`, `kĩ thuật` and `kỹ thuật`, both spellings are correct and the choice carries region and register, so rewriting one into the other is editing the corpus rather than cleaning it. Instead the two fold into one key, deduplication compares keys, and both spellings survive in the text. The fold applies only to the y that is a syllable's whole vowel, since `tay` and `tai` are different words and so are `hay` and `hai`. `quy` folds, because there the u belongs to the consonant.

Normalizing twice changes nothing, and a test says so. Documents pass through this stage every time the corpus is rebuilt, so a rule that kept finding work would invalidate every hash taken on the pass before. The rest of the tests are golden files over real Vietnamese from six kinds of source rather than lorem ipsum with diacritics: an encyclopedia article, a page laid out for print, a news article, a document written under the older tone convention, a forum post full of keystrokes, and a page from before Unicode that is only Vietnamese under one particular font. Each one carries the damage its kind of source actually carries, and `phoi/testdata/README.md` says which document is there for what.

```
document                      changed  repaired  encoding  homoglyphs  invisible  controls  composed  tones  residue  syllables  kept
phoi/testdata/bach-khoa.in    yes      yes                 2           1          0         0         0      0        88         yes
phoi/testdata/ban-in.in       yes      yes                 9           1          0         0         0      0        52         yes
phoi/testdata/bao-dien-tu.in  yes      yes                 0           1          0         0         0      0        76         yes
phoi/testdata/dau-cu.in       yes      yes                 0           0          0         0         6      0        90         yes
phoi/testdata/dien-dan.in     no       no                  0           0          0         0         0      3        66         no, residue
phoi/testdata/font-cu.in      yes      yes       TCVN3     0           0          0         0         0      0        138        yes
6 documents                   5        5         1         11          3          0         0         6      3        510        1 rejected
```

Fixtures say the rules fire. What they cannot say is how much of the corpus this stage is doing anything to, which is the one claim about it that was written down in advance, so the report reads Parquet parts as well as text files and `-total` prints the summary instead of a line for each of a few hundred thousand documents. Reading a part costs 50 MB of resident memory whatever size the part is, because the rows come out of it one at a time and the run holds one. Four parts, one from each source, read back out of the store onto `server2` and measured there while that box was fetching:

```
part                documents  changed  repaired  homoglyphs  invisible  controls  composed  tones   residue  syllables
glotcc/00000        183514     183514   52769     41941       37961      2362      19        136562  24562    248461212
fineweb2/00000      273460     273460   40619     48160       3          0         151       125833  9942     253067746
finepdfs/00000      54676      54676    19494     85842       23289      0         11        178560  17835    214453351
hplt3/00000         54131      54129    17030     7216        1          0         329       85367   8752     160542119
four parts          565781     565779   129912    183159      61254      2362      510       526322  61091    876524428
```

That is four runs of `gao phoi -report -total`, one per part, with the total rows put beside each other. Normalization changed every document in all four, give or take the two HPLT documents that arrived in exactly the form this stage writes. That is true and it is a fact about the final newline rather than about Vietnamese: layout runs on every document that goes past, one that arrives without a trailing newline leaves with one, and a share that rounds to all of them is not telling you anything about the material. So the number to read is the second one, the share of documents where a character was repaired rather than the whitespace settled. It is 23.0 percent across the four parts, and it runs from 14.9 percent on FineWeb2 to 35.7 percent on FinePDFs. The prediction on the board was 3 percent or more, which the stage clears seven times over and which it would also have cleared by touching nothing but whitespace. Splitting the two counts is the fix for a prediction that was too easy to satisfy, and it is a property of the result rather than a note in a release, so the next person to quote the number gets the one that means something.

What the four sources disagree about is the cleaning somebody else already did. FineWeb2 arrives with 3 invisible characters in 273460 documents and HPLT v3 with 1 in 54131, and neither carries a single control character. GlotCC arrives with 37961 invisible characters and 2362 controls in 183514 documents, and FinePDFs with 23289 invisible characters in 54676. Homoglyphs follow the extraction rather than the source, at 85842 in one FinePDFs part against 7216 in one HPLT part, which is what text recovered from a PDF looks like next to text recovered from HTML. Tone mark placement is the one thing all four carry in quantity, from 85367 syllables in the HPLT part to 178560 in the FinePDFs one, because it is a convention of Vietnamese writing rather than damage, and no pipeline built for English would have had a reason to touch it. That is the argument for this stage in one table: the sources have been cleaned, and none of them has been cleaned for Vietnamese.

The four parts took two hours and one minute of one core between them, for 565781 documents and 876 million syllables, on a box that was fetching a different source at the same time. That is the number to multiply when the corpus goes through rather than four parts of it, and it is why running this on the fleet is a milestone item rather than a footnote.

### Vietnamese that was typed before Unicode

Between about 1993 and 2005 Vietnamese was written with fonts rather than with an encoding. A page picked one of a dozen eight bit charsets, shipped the font that drew it, and the bytes in the file meant nothing without it. TCVN3 is the one the .VnTime fonts are drawn for and it is most of that period's text, VNI-Windows is most of the rest, VPS is what the diaspora press used, VISCII is the one that made it as far as an RFC, and the two BK HCM encodings came out of the university tooling in the south. `phoi` reads all six. None of it is Unicode and all of it is still on the web, in the archives the older half of a Vietnamese corpus comes out of.

It does not arrive as bytes, and that is what decides the design. A crawler that meets a TCVN3 page finds it declared as windows-1252, or declared as nothing and sniffed, and the bytes come out the other side as the characters that encoding draws them as. That is why the damage has a look: `TiÕng ViÖt` is TCVN3 for `Tiếng Việt`, `Hµ Néi` is `Hà Nội`, and `Tieáng Vieät` is the same two words in VNI. So the first thing the stage does is put the characters back to the bytes they were made of, and one reverse table serves both readings, because Latin-1 sends 0x80 to 0x9f to the C1 controls and windows-1252 sends them to 27 printable characters, and nothing is in both sets.

Then it has to pick the encoding and it has to be right. All six occupy the same byte range and every one of them decodes every high byte to some Vietnamese letter, so the wrong choice does not produce nonsense that somebody notices on the way past. It produces fluent looking Vietnamese made of words that do not exist, in a corpus nobody is going to read. The test is therefore not whether the output is Vietnamese letters, which it always is, but whether it is Vietnamese words. The evidence is 48 common function words written with their marks on, and a document is transcoded when one encoding finds at least three distinct ones and at least twice as many as the runner up. Every word on that list carries a letter that is only in the document because an encoding put it there, which is what makes counting them a measurement of the encoding rather than of the language, and it is the exact opposite of the list `sang` matches, which is matched with the marks taken off.

Anything short of that margin arrives at the next stage exactly as it came in. The case worth saying out loud is the page that mixes the two, because `é` and `ô` are ordinary letters in Unicode Vietnamese and also ordinary bytes in four of these encodings, so transcoding a mixed page would rewrite the half that was already right. A document already holding a letter Unicode had to add, `ă`, `đ`, `ơ`, `ư` or anything from the block the tone marks live in, is not a candidate whatever the rest of it looks like.

Transcoding runs before every other rule in the package, and the golden fixture exists to keep it there. VPS and VISCII put letters in 0x80 to 0x9f, which a Latin-1 reading turns into control characters that the repair pass would strip before anybody could tell they had been the letter `ộ`, and both of them use the byte 0x85, which the line ending rules turn into a newline.

Two things the stage gives up are written down rather than left to be discovered. TCVN3 has no capital vowel with a tone mark in it, because headings were set in .VnTimeH, a second font with the capitals drawn in the same slots, so the bytes of `VIỆT NAM` and `việt nam` are identical and the case lived in the font. Everything comes back lower case, and a test asserts the table holds no such letter, so the next person to add one has to decide that on the language's behalf in the open. The other is that nothing below 0x80 is ever rewritten, even though BK HCM1 puts six capitals on `^`, `` ` ``, `{`, `|`, `}` and `~`, because a document the detector was wrong about would come out with its braces turned into vowels. Losing a capital on a page that really was BK HCM1 is the smaller of the two wrongs.

The tables are not typed from memory, and where they came from is part of the claim. UniKey's `vnconv/data.cpp` was parsed programmatically rather than read; an independent transcription in the Rust port `marixdev/vnkey` agrees on all 186 letter entries of every charset, with the differences confined to the western symbol tail that is not shipped here; `iconv` covers TCVN3 as TCVN5712-1 and VISCII, and what it says is committed rather than remembered, a line per letter under `phoi/testdata/legacy`, so the check runs on every machine the tests run on and not only on the one that happened to have iconv on it; and the mojibake above is checked in the tests as characters, so anybody who reads Vietnamese can read what is being claimed. UniKey is GPL-2.0, so its mappings are treated as facts about the encodings and re-expressed in gao's own structure rather than copied. TCVN3 and VNI-WIN are corroborated four ways. VPS and the BK HCM pair rest on two agreeing transcriptions of one lineage, which is the other reason the detector wants a margin before it reads a document as one of them.

That third check found something. VISCII carries all 134 letters of Vietnamese and 0x80 to 0xff is 128 codes, so exactly six letters live below 0x80 and every single code above it is a letter. The table here had 127 of them and left 0xa0 empty, where Latin-1 keeps its non breaking space and where VISCII keeps `Õ`. Two transcriptions of the same lineage agreed on the gap and the arithmetic did not, which is the whole argument for a third source that is not a port of the first two.

The fixtures for this stage now start at the bytes. `phoi/testdata/legacy` holds a document per encoding as the bytes it was written in rather than as the characters somebody decoded it into, because a test that starts from characters has already assumed the answer to the only question being asked. One of them is a real page: it reached the corpus as mojibake, mojibake is reversible, so its bytes are the bytes that were on the wire, and they come back out as the text the older fixture already asserts. Two more were encoded by iconv, which did not get its tables from here. The other four were written from gao's own tables because no second implementation exists to write them, and they prove the detector picks the right encoding out of six candidates rather than proving the table is right. Which file is which is written down beside them, since a fixture that agrees with itself is worth saying so about.

What is not known yet is how much of each source is in a font encoding, which is a number this stage can produce and has not been run wide enough to have. The report breaks it down by name for that reason, and measuring it over the corpus is a fleet item in the milestone rather than something a fixture can answer.

## Judging a page a machine read

A scan is not text until something reads it, and the number everybody uses to say how well it was read does not work on Vietnamese. `soi` is what you do to a grain of rice, hold it up to the light and look for the fracture, and this is the stage that holds a machine's reading up against what the page says.

Start with the shape of the language, which is measured rather than asserted. About a quarter of the characters in running Vietnamese carry a mark of some kind and about a sixth carry a tone. Now take a reading at 2% character error rate, which is better than most engines manage on a clean scan and reads as an easy pass. Suppose all of that 2% is in the tone marks, which is exactly where an engine trained mostly on Latin script puts its errors. A sixth of the characters carry a tone, so 2% of the characters is one tone in eight, which is a wrong word in most sentences.

That reading is not slightly damaged. `ma`, `má`, `mà`, `mả`, `mã` and `mạ` are six unrelated words, and a page that has lost the difference between them is still fluent Vietnamese made of words that exist, so every quality filter downstream passes it. A page with 2% of its letters smudged instead gets caught by the first filter it meets. The two score the same, and one of them quietly poisons a corpus.

So the rates are reported separately and never averaged. Character error rate says how much of the page came through. Diacritic error rate is the share of the page's *marked* characters whose marks did not survive, and the denominator is the decision that makes it worth having: it moves for one reason instead of for every reason at once, and it is about four times as sensitive as a rate over all characters. On the 2% reading above the three numbers read 2%, 8% and 12%, and the last two are the ones somebody can act on.

A Vietnamese letter is taken apart into three things that fail independently. The base letter. The letter's own mark, which is part of its identity rather than decoration on it, the circumflex of `â`, the breve of `ă`, the horn of `ư`, the stroke of `đ`. And the tone, one of six. Reading `ế` as `e` loses two of them, reading it as `ề` loses one, and reading it as `é` loses a different one and produces a real word meaning something else. All three are counted on their own line, because they come from different faults in the engine and they get fixed in different places. `đ` read as `d` has its own line too: it is not a tone error, the stroke survives every amount of Unicode normalization, and it is the single commonest thing a Latin trained engine gets wrong here.

Ngang, the level tone, is a value rather than an absence. That is what lets the confusion matrix say which failure an engine has, and a dropped tone and a swapped tone are different engines. No engine has been run against this yet, so the matrix below is a sketch of the shape rather than a result, and it is the shape that carries the argument.

```
page \ read  ngang  huyền  sắc  hỏi  ngã  nặng
ngang        .      .      .    .    .    .
huyền        2      31     .    1    .    .
sắc          9      .      54   .    .    2
hỏi          4      .      .    12   6    .
ngã          1      .      .    7    9    .
nặng         6      .      1    .    .    28
```

Read the ngang column as the tones the engine could not see and the rest as the ones it could not tell apart. The two need different work: the first is resolution or preprocessing and the second is the model. The `hỏi` and `ngã` block above is the classic one, a hook and a tilde that are a few pixels apart at scanning resolution, and it is also the pair Vietnamese speakers in the south merge in speech, so a reading that confuses them is not always the scanner's fault.

The corner where the ngang row meets the ngang column stays empty on purpose. Filling it would mean counting every space and every consonant on the page, and a matrix whose largest number is the spaces is a matrix nobody reads.

```
gao soi page.txt reading.txt                 # the two rates, side by side, for one page
gao soi -matrix page.txt reading.txt         # and what each tone was read as
gao soi -gate eval/*.txt                     # exit non zero if it misses the S4 gate, naming what failed
```

Several pairs in one run are one evaluation set and one score over all of it, not an average of per page scores, because a caption and a page of body text are not one vote each.

Two things this does not do. It does not decide whether a reading is good enough: the thresholds live in the S4 milestone and are checked against these numbers rather than inside them. And it does not know what the page said, only what it was told the page said, so every figure here inherits whatever is wrong in the reference transcript. That is why the hand corrected evaluation set is a separate deliverable from the metric that reads it, and why no engine has a number here yet. The metric is written and tested. Measuring an engine with it needs real scans and real engines on `gamingpc`, which is a fleet item.

## Deciding what is a document

Every quality pipeline published in the last few years applies roughly the same dozen heuristics, and every one of them counts space separated tokens and calls the result words. Vietnamese puts a space between syllables. A word is one syllable or two, sometimes three, and the space carries no information about which. That single fact makes the inherited thresholds wrong by construction rather than by a little, and the one that matters most is Gopher's lower bound on mean word length, which removes a document whose mean falls under 3 characters.

The five golden documents in `phoi/testdata` average 3.36 letters per syllable: 3.07, 3.56, 3.45, 3.29 and 3.58. The news article the `sang` tests run against measures 3.32. So the bound that was written to remove gibberish sits a third of a letter under the middle of the Vietnamese distribution, and a pipeline that inherited it would be removing the language a document at a time and reporting a retention figure that looked reasonable. gao's window is 2.0 to 5.5, and the upper bound is the load bearing one, since the longest Vietnamese syllable is seven letters and a document averaging more than five is not made of them. That is what the English fixture fails on, at 4.69.

The n-gram measures needed the same treatment for the same reason. Gopher takes its top gram over 2 to 4 words and its repeated grams over 5 to 10, so `sang` takes 3, 5 and 7 syllables and 8, 12 and 17, which is the same span of language rather than the same count of tokens. Two other things about that measure did not survive being run against real pages. Gopher's formulation multiplies an occurrence count by a gram length, which double counts overlapping occurrences and can report that more than all of a document sits inside one gram: a flattened gold price table measured 1.08. And it returns a large number when every gram occurs exactly once, which is the normal state of a short document, so a 13 syllable photograph caption measured 0.27 with nothing repeated anywhere in it. `sang` measures the share of positions a gram covers, and returns zero when the most frequent gram occurs once. Both departures are written into the package documentation next to the numbers that forced them.

Vietnamese typed without its tone marks is a register and not a defect. It is most of what gets typed on a phone, it is normal in comments and forums, and putting the marks back means guessing which word was meant, which is the same guess `phoi` refuses to make about input method residue. So a document is labeled `present`, `mixed` or `absent` and goes on either way, and the count is in the report because how much of the corpus is unmarked is worth knowing before anybody decides what to do about it. The cost of that decision is that the function word list has to be written with its marks and matched without them, which is a loosening, and the package says so where the list is defined.

The order the checks run in decides which reason a document that fails several of them is filed under, and the reject store's whole value is being able to ask how many documents went for what. Length comes first, because every other measure is a ratio over almost nothing on a 13 syllable caption. Then the checks that say what shape the page is, before the ones that say what language it is in: a navigation bar holds no Vietnamese sentence and no English one either, so filing it under language would be true by accident and useless on purpose. It is a menu, and boilerplate is what a menu is.

```
document      syllables  mean  stop  vietnamese  alpha   bullets  ellipses  duplicate  repeat  diacritics  kept
article.txt   180        3.32  21    100.0%      100.0%  0%       0%        0%         0%      present     yes
unmarked.txt  180        3.32  21    100.0%      100.0%  0%       0%        0%         0%      absent      yes
caption.txt   13         3.77  1     100.0%      86.7%   0%       0%        0%         0%      present     no, short
menu.txt      63         3.63  0     95.2%       67.7%   100.0%   0%        0%         0%      present     no, boilerplate
listing.txt   135        3.48  14    100.0%      94.4%   0%       100.0%    0%         0%      present     no, boilerplate
looped.txt    384        3.35  8     100.0%      100.0%  0%       0%        87.5%      100.0%  present     no, repetition
chanted.txt   238        3.55  3     100.0%      100.0%  0%       0%        0%         90.4%   present     no, repetition
english.txt   118        4.69  0     31.4%       100.0%  0%       0%        0%         0%      absent      no, language
prices.txt    20         3.20  0     40.0%       38.5%   0%       0%        0%         100.0%  mixed       no, short
9 documents   1331                                                                                         2 kept

22.2% of the documents go on to the next stage.
The reject store records the rest as 2 short, 2 boilerplate, 1 language, 2 repetition.
3 of them carry few or no tone marks, which is a label on the row rather than a rejection.
```

The row carries the measurements rather than the verdicts. A corpus that recorded only that a document passed the length filter cannot be refiltered at a different threshold later without going back to text that is no longer on the box, and every threshold here is one the ablation is expected to move. All of them live in one struct for that reason, the length floor is on the command line, and none of them is claimed to be right yet. Two are properties of the language and will not move much. The rest are Gopher's numbers at Vietnamese sizes, which is to say they are the wrong numbers until a curve says otherwise, in exactly the way the deduplication threshold is.

### Which language, and how the question is asked

Every pipeline reaches for fastText here, and it is the right default when the question is which of a hundred and seventy six languages a page is in. That is not the question. This pipeline asks one question with a yes or no answer, and it asks it about a language whose syllables can be listed.

A Vietnamese syllable is an onset from a list of twenty seven, a rhyme from a list of about a hundred and eighty, and one of six tones. Nothing else is one. There is no syllable with two consonants at the end, because the language has none. None ends in s or l or r, because the coda is one of eight sounds and none of them is those. None is written with k before a back vowel, because the orthography spells that sound c there, and the same rule fixes g against gh and ng against ngh with no exceptions anywhere in the language. A syllable that ends in p, t, c or ch takes the rising tone or the heavy one and can take no other, which is a fact about how the language is spoken rather than a rule anybody is taught, and it holds in text typed by people who have never heard it stated. Between them those rules rule out most of what a string of Vietnamese looking letters could be.

So `sang` generates the inventory from its parts, four thousand and twenty two spellings before tones, and identification becomes a lookup. That buys two things a classifier does not give. It does not degrade on a short document, because a lookup has no context to run out of, and a comment is the length most of the social web arrives at. And it does not have an opinion about register, because it never saw a training distribution to have one about.

Register is where the stock models actually fail, and it is why this is worth building rather than buying. Vietnamese typed without tone marks is not damaged Vietnamese, it is how most people type on a phone, and to a character n-gram model it looks like whichever language it saw most of that writes short syllables in Latin letters. Vietnamese engineering writing with the English terms left in is a third English by token and every model trained on clean prose calls it English. Both are Vietnamese, both are large, and a pipeline that drops them drops the registers the written language is actually moving in.

The two registers get two tests. Text carrying its marks is judged on the share of tokens that are inventory syllables as written. Text without them is judged on the share that match once the marks are taken off both sides, and that bar is set higher rather than lower, which looks backwards until you notice that taking the marks off collapses the inventory: `da` is one token and it is đá and dạ and da and đã. The looser test has to be paid for, and it is paid for with a stricter share and with more function words required.

The cost of a lookup is stated where it is defined rather than hidden. The inventory over-generates on purpose, since `bôn` and `quôn` are formed and neither is a word, because a missing rhyme rejects real Vietnamese while an unused spelling costs a fraction of a point on text that has to fail the other checks anyway. And unmarked matching alone admits a great deal, since `dan` and `cam` and `man` are Vietnamese syllables and also words in several other languages, which is why it is never used alone.

The labeled set is fourteen documents in `sang/testdata/langid`, eight Vietnamese and six not, each one written for a case somebody can name, and its README says what each is and why it is there. At the thresholds in the code all fourteen are called right, but the number worth reporting is the separation and not the count: the worst Vietnamese document scores 0.809 and the best of the others scores 0.496. Fourteen documents can be fitted by accident and a threshold sitting a point from a document would be fitted whether anybody meant it or not, so a test fails the build if that gap falls under 0.25. The hardest negative is romanized Chinese written one syllable at a time, which is short open syllables separated by spaces with about half of them also Vietnamese, and it is what sets the unmarked bar.

Two things this does not yet cover. There are no Muong or Tay negatives in the set, and those are the languages most likely to be filed as Vietnamese, because a fixture written by somebody who cannot check it is worse than no fixture. And fourteen hand written documents say nothing about corpus scale, so the claim on the board, that this admits at least two billion tokens stock fastText rejects at precision 0.95, is measured on the fleet against a sampled and labeled crawl. `Limits.Identify` turns the identifier off for exactly that reason, so the same corpus can be run twice and the difference counted.

Nothing in this stage judges quality. A document that goes through has been found to be Vietnamese prose of some length, which is the floor and not the bar.

## Finding the same document twice

The web republishes. A news agency writes an article and forty sites carry it, a forum quotes a post inside a reply to it, a legal notice sits at the foot of every page of a ministry site, and a scraper that took a site twice took the front page twice. None of that is rare and all of it trains. A model sees the duplicated text as many times as the corpus holds it, and what it learns from the repetition is to reproduce it.

Exact copies cost nothing to find. The document id is already blake3 over the normalized text, so two documents that are the same bytes are the same id and a hash table finds every one of them. That is the easy half and it is not the half that matters, because a republisher almost never publishes the same bytes. The headline changes, a credit line goes on the end, and the content management system that received the piece gives it different quotes and different capitals.

The other half is minhash over character five-grams, with a banded index. Each document becomes 128 numbers that stand in for its set of shingles, two documents that share most of their text agree on most of those numbers, and the bands turn "agree on most" into a lookup. The alternative is every pair against every other pair, and at a hundred million documents that is not a slow program, it is a program that does not finish. The shingles are taken over the deduplication key rather than over the text, so everything `phoi` decided a republisher can change is invisible here too: case, punctuation, spacing, and the `i` and `y` spellings that both survive in the text. Digits are kept, because a table of figures with different figures is a different table.

The banding is the threshold, and this is the part that is usually left implicit. A pair whose similarity is `s` agrees on one row with probability `s`, on a band of `r` rows with probability `s^r`, and on at least one of `b` bands with probability `1 - (1 - s^r)^b`. That curve is a step with its knee near `(1/b)^(1/r)`. gao runs 16 bands of 8 rows, which puts the knee at 0.707, and moving the knee means choosing different numbers rather than filtering harder afterwards. The knee is where it is because of what a Vietnamese near duplicate looks like: a syndicated article that kept the body and changed the furniture lands between 0.7 and 0.9, and a forum post quoting two sentences of it lands near 0.3.

Recall is a number rather than an assumption, so the report prints it. At the operating point a pair at 0.9 is proposed as a candidate essentially always, a pair at 0.5 essentially never, and a pair sitting exactly on the knee is proposed about two thirds of the time, which is what a knee means and is worth seeing rather than being surprised by later.

```
documents  exact  near  kept  retention  clusters  largest
7          0      3     4     57.1%      1         4

Two documents are copies of each other at 0.71 similarity or more, over 16 bands of 8 rows.
A pair at 0.71 is found 65.6% of the time and a pair at 0.5 is found 6.1% of the time.
```

The threshold itself is not chosen here. Removing more duplicates is not better past some point, since the corpus starts losing documents that were merely similar, and removing fewer leaves the repetition in. Where that point is, is a question about this corpus and it is answered by training on both sides of it. So `gao xay -curve` produces what each threshold would retain and nothing in the package decides which one to use. The curve is built at 32 bands of 4 rows rather than at the operating point, because its knee is at 0.42 and a pair that was never proposed as a candidate cannot be scored at any threshold. A curve built at the operating banding would report that a threshold of 0.5 keeps exactly what 0.7 keeps, which is a statement about the index rather than about the corpus.

Inside a bucket the members are compared against the first member rather than against each other. Boilerplate produces buckets of thousands, and the quadratic version of that comparison is the run not finishing. What it costs is a pair that lands in one bucket without either of them resembling the member that got there first, and that pair is caught in another band or through a third document, which is the same mechanism the bands are already relying on. The clusters are then closed with union find, the survivor is the longest document with the lowest id as the tiebreak, and the cluster id is the survivor's own id. Keeping the longest is deliberate: near duplicates usually differ by what one of them is missing, a page an extractor truncated or a copy that lost its last paragraph, and the longest is the one the others are missing something from.

The answer does not depend on the order the documents arrived in. Union attaches the lower root, the representative is chosen by a total order rather than by whichever was seen first, and there is a test that runs the same documents forwards and backwards and requires the same clusters with the same identities. A stage without that property produces a different corpus on every rebuild, and every number anybody published about the last one becomes unreproducible.

What is here is a shard, not the corpus. A signature is 1 KB, so four hundred million documents is 400 GB of signatures against a fleet whose largest box has 64 GB. Holding them is what lets one pass over a shard answer at every threshold the ablation asks about, and it is exactly why it does not scale to the whole thing. The corpus scale pass keeps only the band hashes, 128 bytes per document, and works one band at a time from a file sorted on disk in the way `gao dem overlap` sorts document keys. That pass is not written yet. The arithmetic that says it is needed is in the package documentation rather than waiting to be discovered on the box.

### The half document identity cannot see

A legal footer repeated on every page of a ministry site is not a duplicate document. It is a duplicate paragraph inside documents that are otherwise distinct, so every one of those pages survives the pass above and the footer arrives once per page. A site with forty thousand pages contributes its notice forty thousand times, which is more copies of that sentence than the corpus holds of any sentence somebody wrote on purpose. `gao xay -boiler` is the pass for that, and it is host aware, which is the whole design. "Đọc thêm" repeated across one site is that site's furniture. The same two syllables repeated across the corpus are Vietnamese, and a pass that counted globally would take the language out a phrase at a time and report a retention figure that looked reasonable.

The unit is the line rather than the blank line separated block. After `phoi` a document is lines with the layout settled, and lines are what the extractors emit: a nav column is one line per item, a footer is a line, a share prompt is a line. Blocks would glue the whole column into one lump that matches the column on no other page, which is the shape of furniture that gets missed rather than removed. Lines are compared by the deduplication key, so the same footer under two content management systems is one footer. Blank lines are left where they are, because layout was settled upstream and counting them would make the empty line the most repeated line on every site in the corpus.

It reads the parts twice. A line cannot be known to repeat until the rest of the host has been seen, so the first pass counts and the second strips, and the second is where the text is at hand and the samples for the report are taken. What the first pass holds is one counter per distinct line per host, keyed by a 64 bit hash rather than by the line itself, which is the same trade the shingles make. A line that appears twice inside one document counts once: repetition inside a document is a different problem with its own measure in `sang`, and counting it here would let one page argue that its own refrain is the whole site's furniture.

```
host        documents  distinct lines  furniture  removed  example
vnbao.vn    6          10              4          24       Bản quyền thuộc về báo điện tử. Nghiêm cấm sao …
diendan.vn  1          2               0          0

Across 2 hosts and 7 documents, 24 of 32 lines were furniture, which is 75.0% of them.
A line is furniture on a host with 5 documents or more when it appears in 3 of them or in 10.0%, whichever is more.
```

Three numbers decide what furniture is, and all three are defaults rather than findings, in the way every threshold in this pipeline is a default until an ablation moves it. A host needs 5 documents before anything it repeats is treated as furniture, because three pages agreeing on a sentence is not evidence and a corpus that trimmed on that evidence would be trimming noise. A line needs to appear in 3 of the host's documents and in 10% of them, whichever is more, which is the same rule stated twice so that it survives a host being large: a sentence on three pages of a thousand is not that site's furniture. The pass runs after deduplication by document, and that order is load bearing. A host whose pages are near copies of each other would have every line of them repeating, and a boilerplate pass run first would empty all of them.

A document that was nothing but furniture is counted and named rather than dropped quietly. That page is a real thing on the web, it is a nav column and a footer with no article between them, and it belongs in the reject store with the rest of the record. `gao xay -boiler` takes parts rather than text files, because boilerplate is found per host and a text file carries no host to be aware of.

Run on `server2` over one part from each of two sources, the pass reports this:

```
part            hosts  documents  documents on a host of 5 or more  lines     furniture  removed  emptied
glotcc/00000    60504  183514     101103                            11161135  263340     2808296  5246
fineweb2/00000  95814  273460     145151                            6381092   10986      69915    19
```

The two sources disagree by a factor of twenty three and the disagreement is the finding. A quarter of GlotCC's lines were furniture and 5246 of its documents, one in 35, were nothing else. FineWeb2 lost 1.1% of its lines and 19 documents in 273460. Neither number is about Vietnamese. GlotCC hands over what its extractor pulled off the page with the site still around it, and FineWeb2 hands over text that somebody else already ran a boilerplate pass on, so the 1.1% is not this pass failing on FineWeb2, it is this pass agreeing with the pass that already ran. That is the same shape as the normalization counts: what the stage finds is mostly a measurement of how much cleaning the upstream did.

Both numbers are floors, and the reason is worth stating rather than discovering later. A part is a slice of the crawl and not a slice of a host. FineWeb2's part holds 273460 documents across 95814 hosts, which is 2.9 documents per host, and GlotCC's holds 3.0. A little over half the documents in each part sit on a host that clears the five document floor at all, and the rest are on sites the part saw once or twice, whose furniture is invisible to a pass that only has the part. Grouping the corpus by host before this runs is the open item, and it wants the same sort on disk that the corpus scale minhash pass wants, so the two should be built together rather than twice.

The cost is two streaming passes and the counters, which is what makes it affordable: 12 minutes for GlotCC's part and 14 for FineWeb2's on `server2`, at around 120% of a core, and 705 MB and 635 MB of resident memory. The memory is the counters and the host names rather than the text, so it grows with how many distinct lines a part holds and not with how large the part is.

## Covering what belongs to somebody

Every identifier a person in Vietnam carries is a run of digits, and so is every price, date, document number, flight number and lottery result on the same page. A detector that matches digit runs has enormous recall and no precision at all, and what it produces is not a private corpus, it is a corpus with holes punched through the middle of ordinary Vietnamese sentences. So `che` validates the structure of what it matched. A national ID is twelve digits opening with one of the sixty three province codes the numbering was built on. A mobile number is ten or eleven digits whose first two after the leading zero were assigned to a carrier, and the ones that were never assigned are the reason a ten digit number is not automatically a phone number. A tax code carries a check digit over the weights 31, 29, 23, 19, 17, 13, 7, 5, 3. A plate has a letter series wedged between a two digit province prefix and its tail, which no ordinary number in Vietnamese text does.

The polarity of every one of those rules is chosen the same way, and it is the only design rule in this package that matters. A rule that is wrong should cost precision, which means a number that was not personal gets covered and one sentence reads slightly worse. A rule that is wrong the other way costs recall, which means a real national ID stays in a published corpus and stays there permanently. So a validator is never the only thing standing between an identifier and publication: a cue in the text is sufficient on its own, and the validator only decides whether a bare number with nothing around it is covered as well. The tax check digit can add coverage and can never remove it. The old nine digit CMND has no province table worth defending, so it is found by its cue and nothing else, and that is written down as a known gap rather than papered over with a length test that would cover every nine digit number in the corpus.

Names are not redacted wholesale, and that is a decision rather than an omission. A Vietnamese corpus with no Vietnamese names in it is not a Vietnamese corpus: it cannot say who wrote Truyện Kiều, it has never seen the name of a president, and the names it does not know are exactly the ones a model needs in order to write Vietnamese about Vietnamese people. Half the country is named Nguyễn and redacting Nguyễn is not privacy work. What is actually private is a name beside a way of reaching the person it belongs to, so a name is covered when the paragraph it sits in also holds an identifier that was covered, and left alone otherwise. A news story about Hồ Chí Minh and Nguyễn Du comes back untouched. A classified advertisement loses the seller.

Two things break that rule if nobody guards them, and both are ordinary in Vietnamese. Streets, wards and schools are named after people, so an address is not allowed to donate its street name to the name detector. Companies are routinely named after whoever founded them, and a company is not a natural person, so a business marker suppresses the name that follows it, scoped to the line it appears on. That last scope is the whole difference between a contract whose first line reads Công ty TNHH Thương mại Hoàng Long and whose fourth line names a person: the company keeps its name, and Trần Thị Hương four lines down does not.

Text typed without tone marks is handled the same way `sang` handles it, because it is the same half of the corpus and it is the half with phone numbers in it. A contact block that reads `lien he anh Nguyen Van Minh` is personal data written by somebody in a hurry, and a surname list matching only Nguyễn would find nobody in it. So every word list here is held twice, once as it is written and once with the marks off, and `phoi.Bare` is shared with `sang` rather than reimplemented. The cost is stated where the list is defined: a bare form matches every marked word it could have come from, which is acceptable for a name list already guarded by co-occurrence and would not be acceptable anywhere else.

The address gazetteer holds sixty three provinces and thirty four. Vietnam merged its provincial units on 1 July 2025, and most of the text in the corpus was written before that, so a gazetteer holding only the current thirty four would fail to close an address chain on the majority of the addresses it meets. Holding both costs a longer list and nothing else. The CCCD province codes are the separate case and they stay at the pre 2025 sixty three, because an ID that has been issued does not renumber when the provinces do.

```
document      spans  covered  cued  email  phone  cccd  cmnd  tax  plate  name  address
advert.txt    5      5        0     1      1      0     0     0    1      1     1
contract.txt  5      5        3     0      1      1     1     1    0      1     0
article.txt   0      0        0     0      0      0     0     0    0      0     0
prices.txt    0      0        0     0      0      0     0     0    0      0     0
forum.txt     3      3        0     0      1      0     0     0    0      1     1
5 documents   13     13       3     1      3      1     1     1    1      3     2

60.0% of the documents hold personal data of some kind.
L2 covers all 13 of them.
3 of them were found because the text named what they were, and the other 10 from their own structure.
```

The two documents with nothing in them are the ones that decide whether this stage is usable. `article.txt` is a news story quoting a population figure, a gold price and a year of birth, and naming three people. `prices.txt` is five lines of nothing but numbers, including a ten digit sum of money that a tax detector without a money check would have tagged. Both come back byte for byte identical. The last line of the report is the one worth watching over a real corpus: three of thirteen spans were found because somebody wrote `Mã số thuế` or `CMND` in front of their own number, which means the other ten depend entirely on the structure rules, and it is the number that says what the recall would collapse to if the cue lists were all there was.

What is covered is a level, and the level is recorded per document rather than per run, because a corpus assembled from sources at different levels is a corpus where "what was removed from this document" has a per document answer. L0 finds everything and covers nothing, which is what a source gets measured with before anybody decides what to do about it. L1 covers identifiers and is the level for text whose upstream already published it under its own terms. L2 adds street addresses and the names beside an identifier, and is the level for anything that came out of our own crawl, where nobody upstream made the decision on our behalf.

Nothing found is deleted. A phone number replaced by zeros teaches a model that phone numbers are zeros, a phone number replaced by nothing teaches it that sentences about calling somebody end in the middle, and a phone number replaced by `[SODIENTHOAI]` teaches it that a phone number goes there, which is the only one of the three that is true. The tags are uppercase Vietnamese without tone marks in brackets, so they survive normalization unchanged and cannot be produced by ordinary text.

```
Bán căn hộ 2 phòng ngủ, diện tích 68m2, tại [DIACHI].
Giá 5.200.000.000 đồng, có thương lượng cho khách thiện chí.

Liên hệ chính chủ anh [HOTEN], điện thoại [SODIENTHOAI], hoặc email [EMAIL].
Xe đưa đón xem nhà biển số [BIENSO], đi lại thuận tiện trong nội thành.
```

### How much it actually finds

Precision can be read off a run over real pages, which is what `-spans` is for, and it prints the matched text to the terminal on purpose because reading the matches is the only way to see a detector firing on something it should not. Recall cannot be read off anything. It needs text where somebody has already said what is in it, so `che/testdata/recall` holds twelve documents with the personal data marked by hand, in the text, and `gao che -recall` reports what each detector found of what was marked.

```
detector  marked  covered  recall  found  precision
email     6       5        83.3%   5      100.0%
phone     16      16       100.0%  16     100.0%
cccd      2       2        100.0%  2      100.0%
cmnd      3       2        66.7%   2      100.0%
tax       2       2        100.0%  2      100.0%
plate     2       2        100.0%  2      100.0%
name      10      10       100.0%  10     100.0%
address   8       7        87.5%   7      100.0%
all       49      46       93.9%   46     100.0%
```

The marks sit in the text as `{{kind:text}}` rather than in a second file of offsets, because a set of offsets rots the first time somebody fixes a typo in the document it points into, and nobody notices until it has been two characters out for a year. A marked span counts as covered only when a found span of the same kind holds all of it, since half a national ID with the province code still attached has reached the corpus. What is marked is what the policy says must be covered rather than every proper noun on the page: the news story names a director, a vice chair and a department, none of them are marked, and a detector that fires on any of them fails.

Three of the twelve documents have nothing marked in them, which is where the precision column comes from. One is that news story. One is a builder's price list, six product codes of seven digits each sitting next to prices, which is what a nine digit ID detector without a cue requirement would light up on. One is a quarterly financial summary carrying document numbers, a twelve digit customs declaration and `5260181597 đồng`, which is a structurally valid tax code followed by the word for money.

The measurement paid for itself by finding five defects, all fixed in the detectors rather than by editing what was marked. The address chain read `đường dây nóng` as a street, because a hotline is literally a line and `đường` opens an address, then walked on through the phone number that followed and got it dropped for overlapping. A tax code was filed as a phone number because a phone cue sat earlier in the same window and that branch happened to run first, so cues are now compared by distance and the nearest one wins. `Lê Hoàng Nam` came out as `Lê Hoàng`, because the exclusion list held `Năm`, the word for year, and was matching it with the marks off where the two words are the same string. And `Chủ quán tên Trịnh Văn Đức` produced no name at all, because `quán` marks a business, so the words that introduce a person are now checked before the words that introduce a company.

The fifth is the one worth reading twice, because it came from the Windows leg of CI rather than from the detectors and nothing about it looks wrong. The co-occurrence scope for a name is a paragraph, a paragraph ends at a blank line, and a blank line was being read as two newline bytes in a row. Text with `\r\n` endings has none of those, so the whole document collapsed into one paragraph and one phone number anywhere on the page made every name on it a candidate. The motorbike advertisement gave up `Hà Nội` out of its headline. Name precision fell from 1.000 to 0.714 on the same twelve documents, and on the crawl it would have fallen quietly on every page that happened to be written on a machine that ends its lines the other way. A blank line is now a line with nothing on it but space, and the set is measured both ways with the same numbers required.

Three spans are still not found, each one a class rather than an accident, and the test fails if a fourth appears. An email address written `(a)` instead of `@` to get past a site's own filter, which is how a large share of them are written in classified listings. A bare nine digit CMND with nothing naming it, which is the gap the package already documents, and closing it would cost exactly the precision the price list measures. And a rural address with no house number for the chain to open on, which is the one most worth fixing and needs its own measurement first.

Twelve documents and forty nine spans say the detectors work on the shapes somebody thought of, and a redaction pass is judged on the shapes nobody thought of. What this set does is stop the detectors regressing between fleet runs and record which cases were considered when the current trade was chosen. The number for the corpus is measured on the fleet, against a sample drawn from a real crawl and read by hand afterwards.

## Picking the grit out of the rice

A model trained on a corpus holding its own test set scores well and has learned nothing, and the number that comes out is not wrong by a little. `nhat` is nhặt sạn, picking the grit out of the rice, and the grit is the only contaminant in this pipeline that got into the corpus by being wanted somewhere else: a benchmark item published on the web, quoted in a blog post, argued about on a forum, or scraped into a dataset that was scraped again.

It runs last and it runs again at every release. Every other stage takes the corpus as its input. This one takes the corpus and a list of benchmarks, and the list changes without the corpus changing, because a benchmark published next year is a benchmark this corpus has to be checked against. Running it last means a new benchmark costs one scan rather than a rerun of everything.

The check is thirteen gram exact overlap, and thirteen is a number the field settled on while counting English words. Vietnamese writes a space between syllables rather than between words, so thirteen of what lies between the spaces is about eight words of Vietnamese. This check is therefore stricter than the English one it is borrowed from, which is the direction to err in: a false flag costs one person reading one document, and a miss costs a published score that is not real. Grams are taken over the deduplication key, the same one `gao xay` uses, so an item and a copy of it that changed the quotes, the capitals or the i and y spelling are the same text here. A decontamination check that could be defeated by the things a republisher changes would be a check against careful copying only.

One shared window is reported and three are removed. Windows overlap, so three of them is one run of fifteen consecutive syllables rather than three separate quotations, and a document with one window from each of three unrelated benchmarks is three coincidences rather than a leak. The count is per benchmark for the same reason. A window that two benchmarks share is attributed to both, and a document that repeats the same line ten times reports the overlap once, because otherwise a page with a refrain would cross the threshold on a single shared sentence.

Two files rather than one, and the split is what makes the only-grows rule checkable. The roster is `nhat/benchmarks.json`: names, revisions, the address each revision can be asked for, where the items come from, what part of an item goes in, whether the benchmark is native Vietnamese or translated. It is small, it is read by people, it goes into a release note. The list is the roster with every item's text filled in, which is tens of megabytes of other people's test sets and belongs in a build artifact. A run checks its list against the roster before the scan starts, because a benchmark that failed to fetch produces exactly the report a clean benchmark produces.

```
benchmark       origin      revision      home                                      drops at            source
vmlu            native      b0225316f4ea  git:https://github.com/ZaloAI-Jaist/VMLU  3 windows           ZaloAI-Jaist/VMLU, the public evaluation split
vimmrc          native      b017d98136a6  hf:uitnlp/vimmrc2.0                       3 windows           the UIT NLP group's own copy of ViMMRC 2.0 on the Hub
uit-viquad      native      unpinned      none                                      3 windows           UIT NLP group, UIT-ViQuAD 2.0
mmlu-vi         translated  18e6c8e65b20  hf:alexandrainst/m_mmlu                   3 windows           the Vietnamese config of m_mmlu, run by the harness as m_mmlu_vi
vi-cloze        native      unpinned      none                                      1 window, held out  built by gao, doc 10 section 2.2
vi-diacritic    native      unpinned      none                                      1 window, held out  built by gao, doc 10 section 1.2

Roster 2026-08-06, 24 benchmarks. It only grows.

12 of them have no revision pinned. A release cannot go out until they do, because a release note that says a benchmark was checked has to say which revision of it was checked.

gsm8k-vi: There is no Vietnamese GSM8K to pin. MGSM, which is where lm-evaluation-harness keeps translated grade school arithmetic, covers eleven languages at v0.4.12 and Vietnamese is not one of them. This row names a benchmark that has to be found or built, not one that is waiting on an address.

uit-viquad: UIT-ViQuAD 2.0 is handed out on request by the UIT NLP group and every copy on the Hub is somebody else's upload. Pinning one of those would pin the upload rather than the benchmark, which is a weaker claim than a release note makes. This waits for an address the authors answer for.
```

A revision here is an object id and an address to ask for it, and both halves are required. That rules out the thing the roster used to carry, which was `2.0`: a version number is a name, the files behind a name can be reuploaded, and a release note saying the corpus was checked at 2.0 is a claim a reader cannot go and verify a year later. So the roster takes forty hex characters or the word `unpinned`, and an entry that is unpinned has to say what it is waiting for. Twelve of the twenty four are pinned today. The other twelve each carry a sentence, and printing those sentences is more useful than printing the count, because twelve names look like one problem and the reasons turn out to be four.

Three of the reasons are the same finding, which is that `gsm8k-vi`, `math-vi` and `winogrande-vi` name Vietnamese translations that do not exist. The evaluation harness has Vietnamese ARC, HellaSwag and MMLU through the okapi set, and at v0.4.12 it has no Vietnamese GSM8K, no Vietnamese MATH and no Vietnamese Winogrande. Those three rows stay on the roster with the gap written down rather than quietly disappearing, because a row that is hard to fill is not a row to delete, and a test asserts they keep saying so.

The benchmarks gao builds for itself are the interesting rows. `vi-cloze` and `vi-diacritic` are made by holding text out of `gao-web`, so overlap with them is not evidence of contamination, it is the hold out that did not happen, and one shared window is enough to drop the document rather than report it. `uit-viquad` is the other direction: its contexts come from Vietnamese Wikipedia, which is in the corpus on purpose, so it is expected to come back contaminated and the useful number is how much. `vi-longdoc-qa` is the case where the right answer is to do nothing, because its documents are statutes and theses that belong in the corpus, and it is the questions written about them that the model must not have seen.

```
benchmark  origin      revision      items  found  share   documents  dropped
vmlu       native      b0225316f4ea  2      2      100.0%  2          2
vimmrc     native      b017d98136a6  1      0      0%      0          0
mmlu-vi    translated  18e6c8e65b20  1      0      0%      0          0

4 documents checked against 3 benchmarks over 24 windows, roster demo, list demo-1.
2 documents share text with a benchmark and 2 of them share enough to be removed.
The contaminated benchmarks stay in the eval table with the contamination written next to them. Dropping one quietly is how contaminated scores become published scores.

de-thi.txt, 4 of 22 windows in the list
  vmlu             4 windows from 1 item, dropped

bai-giang.txt, 6 of 14 windows in the list
  vmlu             6 windows from 1 item, dropped
```

The second document is the one worth looking at. It writes `Nguyên lí` where the benchmark writes `Nguyên lý`, both are correct under different orthographic reform positions, and the fold is why it was found anyway. Every benchmark on the roster gets a row whether anything touched it or not, including the ones that came back clean, because a table holding only the contaminated ones cannot be read as a clean bill of health for the rest. Finding contamination is a result rather than an error and the command exits zero when it does.

Two open items, and they are both about the check being weaker than the number suggests. The embedding neighbor check is not written: n-grams cannot see a benchmark item that was translated or paraphrased into the corpus, and for the six translated benchmarks on the roster, which reached Vietnamese through somebody else's translation of the same English source, that is the channel that matters most. It needs an index this project does not have yet. The second is that half the roster is still unpinned, and a revision that is not pinned is a release note that cannot say which items were checked. Both are printed by the tool rather than left for a reader to notice.

## The one task the corpus answers for free

Every evaluation set in this project costs somebody a day of reading, except one. Taking the marks off a page of Vietnamese is a function. Putting them back is not. So `phoi.Bare` turns any page in the corpus into a question whose answer is already sitting next to it, exactly, with no annotator and no disagreement to arbitrate. `dau` is the mark, and `vi-diacritic` is the task set it builds.

That makes it the cheapest set here and the most dangerous one. The answers are in the training corpus by construction, so a model trained on gao has read every one of these pages with its marks on. Every item carries the identity of the document it came from for that reason and for no other: the identity is what lets the items be held out before training and what lets `gao nhat` check afterwards that they were. A `vi-diacritic` score from a run that skipped the hold out is a memorization score, and it will look excellent.

A document typed without its marks is refused. Roughly half the Vietnamese written online is typed bare, and such a document is not an answer key, it is a second copy of the question. The floor is a share of marked characters rather than a yes or no, and it sits at 0.12 against a language that runs at about 0.24. The gap is deliberate. A page about a subject short of marked vowels is still Vietnamese, and a floor set at the average would keep the easy pages and throw the hard ones away.

```
gao dau build -o vi-diacritic.jsonl -one-in 100 parts/*.parquet   # turn documents into questions
gao dau baseline -items vi-diacritic.jsonl other/*.parquet        # the two numbers to beat
gao dau grade -items vi-diacritic.jsonl answers.jsonl             # score a model's answers
```

Two floors get published with any result, and both of them are higher than they look.

The first is answering with the question. A model that hands the bare text straight back has restored nothing at all, and on the test fixtures it scores 75.8% character accuracy and gets 10.9% of syllables exactly right, because about one Vietnamese syllable in nine is written with no mark on it in the first place. Any diacritic restoration figure quoted as character accuracy is quoting a number that starts in the seventies.

The second is a table. Count every bare spelling in some other text, answer each one with the marked spelling it most often had, and use no context whatsoever. On 138 spellings counted off four paragraphs it restores 66.2% of the marks and gets 65.9% of syllables right, against 0% and 10.9% for doing nothing. That is the entire task minus the only interesting part of it, and it is strong, because most bare spellings in Vietnamese have one common answer. A model that does not clear the table has learned the dictionary and not the language, and without the table's number printed beside it nobody reading the model's can tell.

The text the table counts must not be the text the items were built from. A table is trivially perfect on the pages it counted, and a baseline measured on its own training data is the same mistake as a benchmark measured on the model's, one level down.

Scoring is the share of the page's marks that came back rather than character accuracy, for the reason above, with `gao soi` doing the counting. An answer is faithful when it is the question with marks added and nothing else, and only faithful answers get their syllables counted. When the bare forms agree the two sequences line up one for one and the comparison is exact, and when they do not, comparing them means aligning them, which puts a judgment inside a number that should not have one. An unfaithful answer is still scored and still reported, because a model that paraphrases a tenth of the time is a fact about the model rather than a fault in the harness.

Sampling is by document identity rather than by a random draw, so `-one-in 100` picks the same hundredth on `server2` as on `gamingpc` with no seed file passed between them. Building the real set over the corpus is a fleet item. The generator, the two baselines and the scoring are written and tested here.

## Where the corpus lives

gao runs on four real machines with 500 GB of free disk between them, and the corpus is 1188 GB of extracted text, 396 GB compressed. It does not fit, and it does not fit by enough that no amount of tidying changes the answer. `gao box` prints the arithmetic.

So the store of record is off-box and the fleet holds a working set. Off-box rather than more disk, because the corpus outlives the machines and disks bought for a rented box cannot be moved, cannot be shared, and are gone when the box is. Object storage rather than a network filesystem, because every access here is a whole shard read or written by name from several machines at once, with no rename, no partial update, and no locking, which is object storage exactly.

Off-box means dataset repos on the Hugging Face Hub, holding Parquet, under the [open-index](https://huggingface.co/open-index) organization. A published Vietnamese corpus has to be on the Hub for anybody to use it, so a bucket alongside it would mean paying to store the same data twice and paying egress to move it between them. Parquet under a snapshot prefix is queryable where it sits, so a question about a column costs one column instead of a download, and the same path serves the fleet, the release, and the reader. `gao kho datasets` prints the repos, what each one holds, and the query that reads it.

```
read_parquet('hf://datasets/open-index/vietnamese-legal-text/data/snapshot=gao-v1.0/*.parquet')
```

The repos are named for the data rather than for the stage that wrote it, because a name like `gao-xay` tells a reader which of our programs ran, which is the one thing they do not care about. Which repo a document lands in is the license position rather than a preference: a public repo carrying text may only carry text the publication posture says ships, and that is checked in code rather than remembered by whoever creates the repo.

Offload is what makes the arithmetic work. A worker writes one shard, pushes it, deletes it, and takes the next, so peak disk is two shards per worker no matter how large the corpus gets. That is 4.1 GB on `server1` against a 90 GB budget, and it is why a fleet with 500 GB of disk can process a corpus several times that size. Nothing on the fleet is authoritative and nothing on it is backed up. Everything there can be refetched from the store, or in the crawl's case is uploaded before it is deleted. One box, `server2`, holds no corpus bytes at all: it has 8 GB free, which is less than the reserve every box keeps, so the arithmetic says no without anybody having to remember to say it.

What a worker pushes is Parquet, which is the second of two storage formats and the only one anybody outside the project sees. Moving a shard through a stage uses segments, JSONL in zstd frames, because six programs append to a shard as it is built and a schema that is one version older still reads. A release is the opposite case: it is read far more often than it is written, and almost every question asked of a corpus is a question about one column. How many restricted documents are there, what is the quality distribution, which hosts dominate. Parquet answers those by reading one column of one row group instead of every byte of every document, and the same file that answers them on the Hub is the file the trainer streams.

Ingestion writes those files as it goes. `gao gat hf -out DIR` decodes a source and writes the documents the contract admits under `DIR`, rolling over to a new part every 1.5 GB of text, which is the compressed shard target multiplied by the ratio the disk budget assumes. It rolls on text rather than on file size because a Parquet writer buffers a row group and compresses it at the boundary, so the size of the file is not known until it closes, and a writer waiting for a size would be waiting on a number that only appears after the decision was needed. One roll per input file, closed before the ledger records that file, so a run that dies mid file leaves no ledger entry and a directory the restart writes over rather than beside.

Adding `-push` sends each part to the store as it closes and deletes the local copy before the next one opens, which is the offload claim stopping being arithmetic and becoming a thing the program does. A part that cannot be pushed fails the file it came from, because a run that carried on would be filling the disk it was supposed to be emptying, and a part that failed to push is the one copy that has to stay. `gao kho push` does the same thing for one file, which is what gets a part off a disk somebody is about to reclaim after an interrupted run, and what puts the files that are not parts up there. Running the same command again after a box reboots is cheap rather than a second upload: the path inside the repo is a function of the source revision, the input file, and the part number, so a part that is already there is recognized by one request, and the Hub keys the bytes themselves by their digest, so even a part whose upload finished and whose commit did not is committed without sending the gigabyte a second time. Nothing about that resume is remembered locally, which is deliberate. A local record of what has been pushed is a second source of truth and it is wrong from the moment a push succeeds and the process dies before the write.

Every repo carries a card, and the card is generated. `gao kho card` renders one from the snapshot manifest: the counts, the breakdown by source and by reject reason, the stages that produced the snapshot and the versions they ran at, the merkle root, and who signed it. A release pushes it with `-push`, and a card that already says the same thing is left alone rather than committed again. The reason to generate it is that a card written by hand describes the release before last. It says forty billion tokens because that was true in March, it lists four sources because a fifth was added after somebody last opened the file, and nothing about reading it tells you which of its numbers have gone stale. A generated card that disagrees with the data is a bug with a test to write rather than an oversight nobody can see. What it does not try to generate is the argument for why the corpus is built this way, which lives here and is linked from the card rather than restated in it.

The columns are the contract, so they are written out in `kho/parquet.go` rather than reflected off the record type, with one test pinning the list and another asserting that every field of the record has a column. A rename that a reader would notice fails a test rather than shipping as a silent break. A repo that withholds text withholds it in the schema: there is no `text` column at all rather than an empty one, so a query that selects it fails at plan time instead of returning blanks that read like documents with nothing in them. Every file also carries the snapshot, the stage, and the box that wrote it in its own footer, so a shard somebody downloaded a year ago still says where it came from without the manifest next to it. `gao kho columns` prints the contract, and given a file prints what that file actually holds.

## Measuring a corpus that is not on the box

Pushing each part and deleting it is what lets four machines process a corpus several times their disk, and the bill for it arrives the moment somebody asks a question about the whole thing. The question that matters most is how much of the five sources is the same document twice. FineWeb2 and GlotCC are both extracted from Common Crawl, so some of the overlap is not in doubt, and how much of it there is decides whether the corpus is the sum of its sources or a good deal less. Nobody publishing a number like that should be estimating it, and downloading 900 GB back to count it properly is not available on this fleet.

It does not have to come back. Document identity is one fixed width column, and a part is Parquet, so a pass can open each part over HTTP, read the `doc_id` chunk of every row group, and never ask for the pages the text is in. What crosses the wire is around thirty two bytes per document, which is roughly 13 GB for the whole corpus instead of 900. This is the argument the columnar format was chosen for, applied to a question about the corpus rather than to a query somebody runs against the release.

The identities go to disk sorted, one file per source, because none of these sets fit in memory. HPLT v3 alone is a couple of hundred million documents and the box reading it has 5 GB of RAM. Two sorted files are intersected by walking them together and five are unioned by walking all five, and the memory that costs is the number of open files rather than anything about the size of the corpus. The sort itself spills runs that do fit and merges them, which is the usual answer and the right one here. A key is the first eight bytes of the document's blake3 rather than all thirty two, which is a four times smaller file and a four times cheaper merge for a collision rate that rounds to nothing: at four hundred million documents the expected number of distinct documents that collide is under one in two hundred, so what comes out is a count and not an estimate at any precision anybody quotes.

One walk answers everything. The key files are sorted, so stepping through all of them together yields each distinct document once along with the set of sources holding it, and that set is every pairwise intersection, the union, and what each source contributes that nothing else does. Five sources is ten pairs, and measuring the pairs one at a time would read the same document three times to learn what the first read already said. Overlap is printed from both sides, because it is not symmetric and the single number is the one that misleads: all of a small source can sit inside a large one while very little of the large one sits inside the small one.

A pass over a few hundred parts gets interrupted, so it is resumable at the part rather than at the source. Each part's keys are written under a working directory and a part that already has its file is skipped, so a run killed after a hundred parts reads the rest and merges. Nothing about that is remembered in a ledger, because the files on disk are the record and a second one would be wrong the first time a process died between the rename and the write.

```
gao dem keys                                # what the store holds, ready to measure
gao dem keys glotcc-abc1234                 # read one snapshot's identities out of the store
gao dem overlap keys/*.keys                 # the matrix, counted rather than sampled
```

## Checking a count somebody else has to believe

Every size in the release notes is produced by the run that wrote the corpus, which makes it a number the project says about itself. The obvious way to check one is to count the text again, and at this size that is a week of somebody's bandwidth, so nobody does it, and a number nobody checks is a number nobody has to be right about. The plan originally said this check should run in under an hour on one machine. That was never true: the fastest box in the fleet counts 4.2 GB of text in 37 minutes, HPLT v3 alone is around 700 GB, and no arrangement of four machines turns a hundred hours into one. What follows is the protocol that can actually be run, in two levels, and neither of them recounts the corpus.

Level one adds up the shape columns. Every document carries `n_chars`, `n_syllables` and `n_tokens` as fixed width columns, so summing them over every part gives the corpus in three of its four published units at twelve bytes per document before the encoding, against the few kilobytes the document is. That is 48 MB for four million documents, and it covers every part rather than a sample of them. What it proves is that the published total is the sum of what is stored, and what it catches is a report written from a run that did not finish, a source counted twice, a part that never reached the store, and arithmetic.

What level one cannot catch is a column that lies. A stage that rewrote text and forgot to recount leaves columns that add up perfectly to a total describing text nobody has. So level two takes a sample of parts, reads them all the way through, counts every document from its own text, and compares the answer with the column sitting beside it. That half is expensive per part, which is fine, because the number of parts is chosen rather than given.

The sample size comes from the bound wanted rather than from what looked like enough. A sample of `k` parts drawn from a population where a fraction `p` is wrong misses every wrong one with probability `(1-p)^k`, so catching one with confidence `c` needs `k >= ln(1-c)/ln(1-p)`. At 99% confidence, missing no more than a fifth of the corpus is 21 parts, a twentieth is 90, and a hundredth is 459. The shape of that is the thing to read off it: halving the share you are willing to miss doubles the sample while tightening the confidence barely moves it, so the cost is driven by how localized a fault you want to catch and almost not at all by how sure you want to be.

Which parts get read is decided by hashing the seed with each part's path, so the same seed gives the same sample on anybody's machine, which is what makes this something a third party can repeat rather than something they have to take on trust. It is deliberately neither the first `k` nor every `n`th, because the order of the listing is the order the parts were written in and both of those systematically miss a bad run of a stage that sits anywhere else. Ranking by path also means a snapshot that grew by a tenth does not resample the nine tenths that were checked last time.

Both levels are resumable at the part, because a pass over a thousand parts will be interrupted. Level one keeps one line of JSON per part in a single file, which is the opposite of what the key pass does and for the opposite reason: a key file is megabytes and belongs on its own, while a thousand forty byte files cost more to list than the work they save.

Three things are worth saying about what this does not do. Neither level catches a corpus that is uniformly a little off, since level one reads the same columns the report was written from and level two would have to read every part, and a bound over how many parts are wrong says nothing about how wrong any one of them is. The byte length of the text has no column, so level one reports no byte count at all rather than deriving one from the character count, which would be wrong by exactly the diacritics and would look like a measurement, and the sample is therefore the only place a bytes per character ratio comes from. And a token count can only be checked against the pinned tokenizer, so a run without one checks two of the three columns and says so in its output rather than reporting a token column that passed.

The check counts with the same two functions the ingest counted with. A verifier that counts its own way is measuring the distance between two implementations, and the question here is whether the column describes the text next to it.

```
gao dem verify                              # what a full check would cost, per snapshot, before running it
gao dem verify -level counts -counts ingest/  # add the columns up and put them against the published counts
gao dem verify -level text -tokenizer tokenizer.model  # and read the sample, checking all three columns
gao dem verify -share 0.01 -seed s1-2026-08 # a tighter bound, and the seed a third party repeats it with
```

## What we may publish

A corpus assembled from four acquisition paths and hundreds of thousands of hosts does not have a license, it has a distribution of them, so every document carries its own license class and the evidence that assigned it. `gao luat` prints the whole position: the determination for each source, what ships for a document of each class, and the questions gao has put to counsel.

The rule is that gao publishes what it may publish and publishes the recipe for the rest. Open and permissively licensed documents ship as full text. Restricted documents, which is where most of the crawl lands, ship as a URL and every metadata column with the text withheld, so somebody else can rebuild the same corpus from the same sources under their own lawful access. Material carrying a machine readable text and data mining reservation ships as nothing at all, and the count of what was withheld goes in the release notes, because a number that quietly disappears reads as a number that was never there. Our headline token count therefore includes tokens we cannot ship, and the release notes state both numbers rather than the flattering one. The projection before the corpus exists is 210B publishable of 300B total.

Two sources are worth calling out. Vietnamese statutes, decrees, circulars, and gazettes are outside copyright protection by statute, which makes a complete, deduplicated, normalized Vietnamese legal corpus fully publishable with nothing attached to it. Vietnamese Wikipedia is the opposite case: its share alike term could propagate to anything it is mixed into, so it stays in its own shard rather than being blended, which keeps the question contained to half a billion tokens instead of raising it over three hundred.

Ten questions are with counsel, and each carries the position gao acts on until an answer arrives. That is deliberate. Legal review here is a check rather than a blocker, and the only way that works is if every question has a written default chosen so that acting on it and being wrong is recoverable: exclude rather than include, redact rather than keep, file rather than wait. One of the ten can change what the project ships rather than a detail of it, and the answer to that one is already written down too. If the text and data mining allowance turns out not to cover model training, gao publishes the URL list, every metadata column and score, the classifiers and their reference sets, and the entire pipeline. The corpus becomes a build script rather than a download, and the project continues at reduced scope rather than ending.

There are two things this project will not do for tokens. No pirated sources: not shadow libraries, not book piracy dumps, not mirrors of paywalled journals, however routine their use has become elsewhere. And no quiet inclusion of reserved material: a reservation is honored, and if counsel says the allowance permits training on reserved text anyway, the model card will say that we did, because the model card is where a rightsholder looks.

Before any of that there is robots.txt, which answers the narrower question of whether a page may be fetched at all. The format is thirty years old and was only written down as a standard in 2022, so the parser has two jobs that pull against each other: follow the specification, and read the file the site actually published. Where they disagree the tie goes to the site. A byte order mark from a Windows editor, a misspelled `Disallow`, a directive shouted in capitals, a comment at the end of the line: all of them are read as what they plainly mean, because a parser strict enough to find nothing in a file has not honored it. The tolerance runs one way only. A misspelled `Disallow` is a disallow, and a misspelled `Allow` is nothing at all, since inventing permission out of a typo is how a crawler ends up somewhere it was not invited.

One case in that parser is about Vietnamese specifically. A site writes `Disallow: /tìm-kiếm` in the file, because that is what it typed, and a crawler asks for `/t%C3%ACm-ki%E1%BA%BFm`, because that is what a URL is. They are the same path, a byte comparison says they are not, and the effect is that every rule a Vietnamese site writes about its own pages quietly stops applying to them. Both sides are encoded the same way before they are compared, and the tests for it are written against paths in Vietnamese rather than against `/foo`.

The other decision worth stating is what happens when robots.txt cannot be read. A 404 means the file is not there and the site has asked for nothing, so everything is allowed. A 429 or a 500 or a timeout means we could not tell, and then nothing is allowed until it can be read, because a crawler that treats "I cannot reach you" as "you did not object" hits hardest exactly when a site is least able to take it.

Honoring a reservation is code before it is a policy. `gat` reads all three of the ways a site can state one: the `X-Robots-Tag` header, the same directives written into a meta element, and TDMRep, both the two response headers and the `/.well-known/tdmrep.json` file, where the longest location that matches a path is the one that applies. What comes back is what the site said, in its own spelling, recorded per fetch and carried with the document rather than folded into a flag, because a decision taken later has to be taken against the statement and not against somebody's memory of it. Two statements about one page combine the restrictive way, since reading a site say no twice and honoring the permissive one is a way of getting to yes. A page that reserves indexing and a page that reserves mining both end up out of the corpus, and the record says which of the two it was, because gao is a training corpus and keeping a page while promising not to train on it is a promise nobody downstream could check.

The statement and the conclusion are two columns rather than one. `tdm_signals` keeps what each mechanism said, and `consent` reduces it to one word: open, no-train, no-index, or empty. Empty is the useful one. It means nobody was there to ask, which is the true state of every document that came out of somebody else's corpus, and it is not the same as a site that was asked and said yes. A row that carries signals and no conclusion is refused by the ingest contract, because that is the shape a dropped honor check takes: the evidence is in the row and the verdict has been quietly softened. The check that keeps a reserved page out of a published file runs at the write into the store rather than only at the fetch, since a reservation honored in one place is honored only while that place is the only way in, and a document can reach the store from a path that predates the column or from a site that has since changed its mind.

The header has one piece of syntax worth naming, since getting it wrong fails quietly and in the flattering direction. A line may open with a crawler name and a colon, and `unavailable_after` carries a colon of its own, so the two are told apart by knowing the directive names rather than by counting colons. A parser that counts colons reads `noindex, unavailable_after: 25 Jun 2010` as a line addressed to a crawler nobody is called and drops the `noindex` with it.

## Why this is not just another crawler

Three problems in Vietnamese text processing are load bearing, and general pipelines get all three wrong.

**Tone marks are the language.** `ma`, `má`, `mà`, `mả`, `mã`, and `mạ` are six unrelated words. A pipeline that drops or misplaces diacritics produces text where a large fraction of words are the wrong word, and character error rate will not tell you, because the damage is only 4% of characters. gao measures diacritic error rate separately and gates on it.

**Two spellings of one word are two cold starts.** Vietnamese accepts two conventions for tone mark placement, so `hoà` and `hòa` are the same word written differently. Left alone they hash to different shingles, survive deduplication as two documents, and occupy two rows in the embedding table. Normalization runs first in gao for exactly this reason, and it is treated as a correctness problem rather than as cleanup.

**Vietnamese PDFs from 1995 to 2012 are a minefield.** Before Unicode adoption was universal, Vietnamese was typeset with one byte font encodings such as TCVN3, VNI, and VPS that map ASCII code points to diacritic glyphs. Those PDFs have a perfect text layer that extracts as `Coäng hoøa xaõ hoäi chuû nghóa Vieät Nam`. gao detects the encoding, transcodes it, and validates the result before admitting it.

There is a fourth, and it is the reason spaces mislead everyone: Vietnamese writes spaces between syllables, not between words. Every heuristic that counts words or measures average word length is measuring something else on Vietnamese text, so every threshold inherited from an English pipeline is wrong by construction.

## Layout

```
cmd/gao/     the single binary
gat/         acquisition: Hugging Face, Common Crawl, crawl, media
dem/         counting: the tokenizer that defines a gao token, and the counts
phoi/        normalization: Unicode, orthography, encoding repair
sang/        filtering: language ID, heuristics, quality classification
soi/         judging a reading: character and diacritic error rates, tone confusion
xay/         milling: deduplication, boilerplate removal
che/         covering: Vietnamese personal data, found and tagged over
nhat/        decontamination: the benchmark roster, and what of it the corpus holds
kho/         the store: records, manifests, snapshots, signing
vo/          the reject store: dropped documents and why they were dropped
doc/         schema and contracts shared across stages
luat/        the legal position: license determinations, publication posture
may/         the fleet: the four boxes this actually runs on
```

Flat packages, no `internal/`, one module, one binary. Crawler code arrives from [openindex](https://github.com/tamnd/openindex) by copy rather than by import, per the standing port discipline.

## Design rules

These are fixed and the rest of the code is downstream of them.

1. **The token is defined before anything is counted.** One gao token is one token under the Gemma-3 vocabulary of 262144 pieces, which on the first counted material is 3.28 characters of Vietnamese against a predicted 3.0. Sizes in gigabytes mean UTF-8 bytes of extracted text only, never parquet size and never the archive.
2. **Natural and synthetic never mix in a headline.** Model generated text is a separate artifact with its own name, its own count, and its own generator card. The corpus size of gao is the natural number.
3. **Provenance is a required column.** Source, URL, crawl timestamp, extraction method and version, every quality score, dedup cluster, and license class. A document that cannot carry provenance is dropped rather than admitted with nulls.
4. **Deduplication is tuned, not maximized.** It is not monotonically good. gao picks its threshold from a measured ablation curve and publishes the curve.
5. **Every release is immutable, content addressed, and signed.** Re-running a stage on the same input produces the same bytes, and there is a command that proves it.
6. **The crawl is polite.** robots.txt, published user agent and contact, crawl delay respected, per host concurrency capped, consent state recorded per fetch. No IP rotation, no user agent spoofing, no paywall circumvention. A block is a stop.
7. **Predictions before measurements.** Every yield estimate, gate, and cost line gets a written prediction before the run, and the prediction stays next to the result, including the ones we got wrong.

## Related

- [openindex](https://github.com/tamnd/openindex) donates the crawler: frontier, fetcher, robots, WARC, and the provenance contract.
- [ccrawl-cli](https://github.com/tamnd/ccrawl-cli) is the Common Crawl surface.
- [hf-cli](https://github.com/tamnd/hf-cli) is the Hugging Face surface.
- [go-trafilatura](https://github.com/tamnd/go-trafilatura) does HTML extraction.
- [luatdo](https://github.com/tamnd/luatdo) builds a Vietnamese legal knowledge graph from gao's legal shard.

## License

MIT. See [LICENSE](LICENSE).
