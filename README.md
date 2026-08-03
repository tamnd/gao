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
gao gat hf     --manifest ingest.hf.toml    # harvest from Hugging Face
gao gat cc     --snapshots all              # recover Vietnamese from Common Crawl
gao gat crawl  --policy crawl.toml          # crawl the Vietnamese web directly
gao gat media  --from crawl                 # fetch PDFs, audio, video

gao phoi       --in raw/ --out normalized/  # dry: Unicode and orthographic normalization
gao sang       --in normalized/ --out kept/ # sift: language ID, heuristics, quality
gao xay        --in kept/ --out milled/     # mill: deduplication, boilerplate removal

gao kho release --snapshot gao-v1.0         # store and publish
gao kho verify  snapshots/gao-v1.0          # check a snapshot against its manifest
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

## Where the corpus lives

gao runs on four real machines with 500 GB of free disk between them, and the corpus is 1188 GB of extracted text, 396 GB compressed. It does not fit, and it does not fit by enough that no amount of tidying changes the answer. `gao box` prints the arithmetic.

So the store of record is an S3-compatible object store and the fleet holds a working set. Off-box rather than more disk, because the corpus outlives the machines and disks bought for a rented box cannot be moved, cannot be shared, and are gone when the box is. Object storage rather than a network filesystem, because every access here is a whole shard read or written by name from several machines at once, with no rename, no partial update, and no locking, which is object storage exactly. S3-compatible rather than one vendor's API, because the protocol is the commitment and the provider is not: the endpoint comes from `GAO_STORE` and switching providers when the egress bill says to is a configuration change.

Nothing on the fleet is authoritative and nothing on it is backed up. Everything there can be refetched from the store, or in the crawl's case is uploaded before it is deleted. One box, `server2`, holds no corpus bytes at all: it has 8 GB free, which is less than the reserve every box keeps, so the arithmetic says no without anybody having to remember to say it.

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
phoi/        normalization: Unicode, orthography, encoding repair
sang/        filtering: language ID, heuristics, quality classification
xay/         milling: deduplication, boilerplate removal
kho/         the store: records, manifests, snapshots, signing
vo/          the reject store: dropped documents and why they were dropped
doc/         schema and contracts shared across stages
```

Flat packages, no `internal/`, one module, one binary. Crawler code arrives from [openindex](https://github.com/tamnd/openindex) by copy rather than by import, per the standing port discipline.

## Design rules

These are fixed and the rest of the code is downstream of them.

1. **The token is defined before anything is counted.** One gao token is one token under a Gemma-3 class 256k multilingual vocabulary, which for Vietnamese is 3.0 characters. Sizes in gigabytes mean UTF-8 bytes of extracted text only, never parquet size and never the archive.
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
