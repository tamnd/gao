# The labeled set

Fourteen documents, eight Vietnamese and six not, each written to be a case the identifier has to get right for a reason somebody can state. The set is small and it is meant to be. It is not a benchmark and no accuracy number taken over it means anything on its own. It is the record of which cases were considered when the thresholds in `identify.go` were chosen, so that moving one of those numbers later is a decision made against a list rather than against a memory.

Every document was written by hand for this repository. None of it was taken off a site, which keeps the license question out of the test corpus and lets each file be exactly the shape it is here to test rather than approximately that shape.

The two things this set is built around are the two mistakes that matter, and they are not symmetric. Vietnamese thrown out is gone, and nobody finds out. Something else let in shows up later as noise in a corpus nobody reads by hand. The eight positives are the cases a stock model throws Vietnamese out on. The six negatives are the cases a syllable inventory lets something else in on.

## Vietnamese

| document | what it is | why it is here |
| --- | --- | --- |
| `ngan.txt` | two sentences of ordinary marked prose | the floor. A document this short has no room for a share to average out, and it is the length most user comments arrive at |
| `dau-cu.txt` | marked prose written throughout in the older tone mark convention | `hoà` and `khoẻ` and `thuỷ` are correct Vietnamese and a model trained on one convention has seen the other rarely |
| `mien-nam.txt` | southern speech written down, `tui` and `má` and `xíu` | the identifier must not be an identifier for the northern written standard |
| `so-lieu.txt` | a statistical report, a fifth of it numerals and units | numbers are not syllables and they lower every share. A page that is mostly a table is still Vietnamese |
| `viet-hoa.txt` | an official notice in full capitals | the inventory is checked lowercased, and this is the document that fails if that step is ever dropped |
| `chen-tieng-anh.txt` | Vietnamese engineering writing with the English left in | the hardest positive. It scores 0.809, the lowest of any Vietnamese document here, and it is what sets `MinRate` at 0.75 rather than higher |
| `khong-dau.txt` | a forum post typed without tone marks | the register this whole piece of work exists for. Every model trained on news text calls it something else |
| `khong-dau-ngan.txt` | two sentences, unmarked | the two hard cases at once, short and unmarked, which is what a phone message is |

## Not Vietnamese

| document | what it is | why it is here |
| --- | --- | --- |
| `tieng-anh.txt` | plain English prose | the baseline. Nothing works if this fails |
| `ten-viet-trong-tieng-anh.txt` | English about Vietnam, thick with unmarked Vietnamese names | the expensive mistake. `Nguyen Van Linh` and `Ho Chi Minh` are Vietnamese syllables with the marks off, which is the exact shape unmarked Vietnamese has |
| `binh-am.txt` | romanized Chinese, written one syllable at a time | the hardest negative. Short open syllables separated by spaces, about half of them also Vietnamese syllables. It scores 0.496 bare and it is what sets `MinBareRate` at 0.90 |
| `phap.txt` | French | Latin script with diacritics, so `MarkRate` alone cannot be the test for which register a document is in |
| `bo-dao-nha.txt` | Portuguese | the same, and it writes `ã` and `õ`, which are Vietnamese letters |
| `indonesia.txt` | Indonesian | Latin script, no diacritics, and a syllable structure that looks Vietnamese in outline until it is checked against the inventory |

## What the set measures

Run `go test ./sang -run TestTheLabelledSet -v` for the verdicts and `-run TestTheLabelledSetIsDecidedByAMargin` for the separation. At the thresholds in `identify.go` all fourteen are called right, and the number worth watching is not that one but the gap: the worst Vietnamese document scores 0.809 and the best of the others scores 0.496. Fourteen documents can be fitted by accident, and a threshold sitting a point away from a document on either side would be fitted whether anybody meant it or not. The margin is the part of the result that is hard to get by luck, and the test fails if it falls below 0.25.

The score in that comparison is `Rate` for a document that carries its marks and `BareRate` for one that does not, because those are the two questions the identifier actually asks. Mixing them into one column would hide the fact that the unmarked documents are being judged by a looser test held to a stricter bar.

## What it does not cover, and why

There are no minority language negatives here. Mường and Tày and Thái are written in Latin script with tone marks, they are the languages most likely to be filed as Vietnamese by anything short of a real classifier, and they are the gap in this set. They are missing because a fixture written by somebody who cannot check it is worse than no fixture: it would pass or fail for reasons nobody could explain, and it would be believed. Filling that gap needs text from a speaker or from a licensed corpus, and until then the honest thing is to say that this set does not test it.

The set also says nothing about how the identifier behaves at corpus scale. It holds a hundred tokens a document and fourteen documents, and the claim on the milestone board is about billions of tokens and a precision figure. That claim is measured on the fleet, against a sample drawn from a real crawl and labeled after the fact, and `Limits.Identify` exists so the same corpus can be run twice with the identifier on and off and the difference counted. Nothing here substitutes for that.
