# Contributing

Thanks for looking. This document covers the conventions that are not obvious from reading the code.

## Getting set up

You need Go 1.26.5 or newer and `golangci-lint`. Nothing else, and no C toolchain, because `CGO_ENABLED=0` everywhere except the race detector job.

```
git clone https://github.com/tamnd/gao
cd gao
make build
make test
```

`make lint` runs the same configuration CI runs, so a clean local run means a clean CI run.

## Code conventions

**Flat packages.** One directory per pipeline stage, named for the rice verb it performs. No `internal/`, no `pkg/`, no `/vN` in the module path. If a package needs a subpackage it usually needs to be two packages.

**One binary.** Everything ships as subcommands of `gao`. New functionality is a new subcommand, not a new `cmd/` entry.

**Errors carry the document.** A failure in a pipeline stage should say which document failed and at what offset. `fmt.Errorf("phoi: %s: %w", docID, err)` is the shape. Wrapped errors are checked with `errors.Is` and `errors.As`, and `errorlint` enforces that.

**Tests are golden files where output is text.** Vietnamese text transformations are much easier to review as a diff than as a table of string literals. Put the input under `testdata/`, run `make golden` to regenerate, and read the diff before committing it. Never regenerate golden files to make a failing test pass without understanding what changed.

**Vietnamese test data is real Vietnamese.** Not lorem ipsum with diacritics sprinkled on. If you are testing tone mark placement, use words where the two conventions actually differ, such as `hoà` and `hòa`. If you are testing legacy encodings, use a real TCVN3 byte sequence.

## Commits and pull requests

Commit subjects are lowercase, imperative, and under 72 characters. The body explains why, not what, because the diff already says what.

One pull request does one thing. If you find a second thing while doing the first, open an issue for it rather than growing the branch.

Every pull request must state which milestone slice it belongs to and which checklist items it closes. CI has to be green before merge, including the cross-platform build matrix, since a change that breaks the riscv64 build is a change that breaks the build.

## Writing

This applies to the README, issues, pull request descriptions, comments, and commit messages.

Write like a developer explaining something to another developer. Concrete over abstract, specific numbers over vague claims, and no filler. Do not use em dashes. Do not break a sentence across two lines in prose files, since it makes diffs noisy and rewraps badly.

If you state a number, say where it came from. Measured, estimated, or quoted from a paper are three different things and the reader needs to know which one they are reading.

## Predictions

The spec keeps a predictions register, and the discipline is that every yield estimate, gate, and cost line gets written down before the run rather than after. If your change adds a threshold or a budget, write the prediction into the milestone issue first. The point is to be able to tell later whether we understood the problem or got lucky, and that only works if the prediction is timestamped by a comment we cannot edit quietly.

## Scope of the crawler

The crawl is polite and that is not negotiable. robots.txt is respected, the user agent is published with a contact address, crawl delay is honored, per host concurrency is capped, and consent state is recorded for every fetch. Patches that add IP rotation, user agent spoofing, paywall circumvention, or any form of block evasion will be closed. A block is a stop.

## Reporting a problem

Open an issue with the input that reproduces it. For text processing bugs, include the exact bytes, since Vietnamese text loses its interesting properties when it passes through a normalizing text box. `xxd` output is fine and often better.
