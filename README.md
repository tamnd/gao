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
gao gat ledger -dir ingest/                 # what the harvest has finished so far
gao gat ledger -dir ingest/ -files          # every finished file, and how each one was read

gao dem model  -o tokenizer.model           # fetch the tokenizer that defines a gao token
gao gat hf     -dir ingest/ -tokenizer tokenizer.model  # and count tokens while harvesting
gao dem counts ingest/                      # what the harvest counted, per source
gao gat cc     --snapshots all              # recover Vietnamese from Common Crawl
gao gat crawl  --policy crawl.toml          # crawl the Vietnamese web directly
gao gat media  --from crawl                 # fetch PDFs, audio, video

gao phoi       --in raw/ --out normalized/  # dry: Unicode and orthographic normalization
gao sang       --in normalized/ --out kept/ # sift: language ID, heuristics, quality
gao xay        --in kept/ --out milled/     # mill: deduplication, boilerplate removal

gao kho release --snapshot gao-v1.0         # store and publish
gao kho verify  snapshots/gao-v1.0          # check a snapshot against its manifest
gao kho datasets                            # where processed data is written, and how to read it

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

The columns are the contract, so they are written out in `kho/parquet.go` rather than reflected off the record type, with one test pinning the list and another asserting that every field of the record has a column. A rename that a reader would notice fails a test rather than shipping as a silent break. A repo that withholds text withholds it in the schema: there is no `text` column at all rather than an empty one, so a query that selects it fails at plan time instead of returning blanks that read like documents with nothing in them. Every file also carries the snapshot, the stage, and the box that wrote it in its own footer, so a shard somebody downloaded a year ago still says where it came from without the manifest next to it. `gao kho columns` prints the contract, and given a file prints what that file actually holds.

## What we may publish

A corpus assembled from four acquisition paths and hundreds of thousands of hosts does not have a license, it has a distribution of them, so every document carries its own license class and the evidence that assigned it. `gao luat` prints the whole position: the determination for each source, what ships for a document of each class, and the questions gao has put to counsel.

The rule is that gao publishes what it may publish and publishes the recipe for the rest. Open and permissively licensed documents ship as full text. Restricted documents, which is where most of the crawl lands, ship as a URL and every metadata column with the text withheld, so somebody else can rebuild the same corpus from the same sources under their own lawful access. Material carrying a machine readable text and data mining reservation ships as nothing at all, and the count of what was withheld goes in the release notes, because a number that quietly disappears reads as a number that was never there. Our headline token count therefore includes tokens we cannot ship, and the release notes state both numbers rather than the flattering one. The projection before the corpus exists is 210B publishable of 300B total.

Two sources are worth calling out. Vietnamese statutes, decrees, circulars, and gazettes are outside copyright protection by statute, which makes a complete, deduplicated, normalized Vietnamese legal corpus fully publishable with nothing attached to it. Vietnamese Wikipedia is the opposite case: its share alike term could propagate to anything it is mixed into, so it stays in its own shard rather than being blended, which keeps the question contained to half a billion tokens instead of raising it over three hundred.

Ten questions are with counsel, and each carries the position gao acts on until an answer arrives. That is deliberate. Legal review here is a check rather than a blocker, and the only way that works is if every question has a written default chosen so that acting on it and being wrong is recoverable: exclude rather than include, redact rather than keep, file rather than wait. One of the ten can change what the project ships rather than a detail of it, and the answer to that one is already written down too. If the text and data mining allowance turns out not to cover model training, gao publishes the URL list, every metadata column and score, the classifiers and their reference sets, and the entire pipeline. The corpus becomes a build script rather than a download, and the project continues at reduced scope rather than ending.

There are two things this project will not do for tokens. No pirated sources: not shadow libraries, not book piracy dumps, not mirrors of paywalled journals, however routine their use has become elsewhere. And no quiet inclusion of reserved material: a reservation is honored, and if counsel says the allowance permits training on reserved text anyway, the model card will say that we did, because the model card is where a rightsholder looks.

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
xay/         milling: deduplication, boilerplate removal
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
