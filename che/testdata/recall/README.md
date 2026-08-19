# The labeled set

Twelve documents with the personal data in them marked by hand, in the text, as `{{kind:text}}`. Forty nine spans across eight detectors, and three documents with nothing marked in them at all. `recall.go` takes the marks out, hands the plain text to the detectors, and reports what each detector found of what was marked.

Every document was written by hand for this repository. None of it was taken off a site, which matters more here than it does for a language identifier: a labeled set for a redaction pass built out of real classified advertisements would be a file of real people's phone numbers checked into a public repository. The names, the numbers and the addresses here are invented, and the numbers were chosen to be structurally valid so that the detectors are tested on the thing they check rather than on a validator failing.

## Why the marks are in the text

A labeled set held as offsets in a second file rots the first time somebody fixes a typo in the first one, and nobody notices until the offsets have been pointing two characters to the left for a year. Marking a span where it sits keeps the label attached to the thing it labels, and it keeps each fixture readable as a page rather than as a table of byte ranges.

## What is marked

What the policy in `che.go` says must be covered, and not everything a person could point at.

Names are the case where that distinction does the work. A name beside a way of reaching the person is marked, because L2 covers it. A name in a news article is not marked, because covering that is the thing this package refuses to do: a Vietnamese corpus with the Vietnamese names taken out of it cannot say who wrote a poem. So `ban-tin-thoi-su.txt` names a director, a vice chair and a department, and none of them is marked, and a detector that fires on any of them fails the measurement.

Read the recall figure with that in mind. It is recall against a policy, not recall against every proper noun in the text, and reading it the other way makes it say something it does not say.

## The documents

| document | what it is | why it is here |
| --- | --- | --- |
| `rao-vat-nha-dat.txt` | a house for sale | the shape the whole package was written for. A name, two phone numbers and a full address chain in three lines |
| `rao-vat-xe-may.txt` | a motorbike for sale | two registration plates in the two formats the plates come in, one of them in a sentence about a second bike rather than in a contact block |
| `hop-dong-thue-nha.txt` | a tenancy agreement | both national ID numberings in one document, twelve digits under the new scheme and nine under the old, each named by the text |
| `thong-bao-doanh-nghiep.txt` | a company change of address notice | a tax code, a company landline and a director. The company name is not marked, because a company is not a person |
| `ho-so-ung-tuyen.txt` | a job application | a national ID, a personal email, a home address and a second person named as a reference, which is the case where somebody else's phone number is in your document |
| `chan-trang-lien-he.txt` | the contact block of a public office | a switchboard, a hotline and a branch, and the document that caught the address chain walking through `đường dây nóng` into a phone number |
| `dien-dan-khong-dau.txt` | a forum post typed without tone marks | the unmarked register. `Ngo Thi Thu` and `so 7 ngo 22 duong Kim Giang` have to be found by the same detectors that find them marked |
| `tin-rao-ban-dat.txt` | land for sale | an international format phone number, and a rural address with no house number to open the chain |
| `rao-vat-ne-bo-loc.txt` | a cafe lease transfer | the hard cases on purpose. An email written to get past a site's filter, a phone number written with dots, a bare national ID with no cue, and a phone number spelled out in words |
| `ban-tin-thoi-su.txt` | a news story | nothing marked. Three officials named, dates, and four money figures, and every one of them has to survive |
| `bang-gia-vat-lieu.txt` | a builder's price list | nothing marked. Six product codes of seven digits each, sitting next to prices, which is what a nine digit ID detector without a cue would fire on |
| `so-lieu-va-ma-so.txt` | a quarterly financial summary | nothing marked. Document numbers, a standard reference, a twelve digit shipping code, a twelve digit customs declaration, and `5260181597 đồng`, which is a structurally valid tax code followed by the word for money |

## What it measures

Run it two ways. `go test ./che -run TestTheRecallOfEachDetector -v` prints the table whether it passes or not, and `gao cover -recall` prints the same thing from an installed binary, which is the point of embedding the set rather than reading it off disk.

| detector | marked | covered | recall | found | precision |
| --- | --- | --- | --- | --- | --- |
| email | 6 | 5 | 83.3% | 5 | 100% |
| phone | 16 | 16 | 100% | 16 | 100% |
| cccd | 2 | 2 | 100% | 2 | 100% |
| cmnd | 3 | 2 | 66.7% | 2 | 100% |
| tax | 2 | 2 | 100% | 2 | 100% |
| plate | 2 | 2 | 100% | 2 | 100% |
| name | 10 | 10 | 100% | 10 | 100% |
| address | 8 | 7 | 87.5% | 7 | 100% |
| all | 49 | 46 | 93.9% | 46 | 100% |

A marked span counts as covered only when a found span of the same kind holds all of it. Covering part of it does not count, and that is deliberate: the point of a span is that the text inside it does not reach the corpus, and half of a national ID with the province code still attached has reached the corpus. Partial coverage is recorded separately, because a phone number covered except for its last two digits is a boundary bug rather than a validator bug and the two get fixed in different places.

The precision column is measured mostly by the three documents with nothing in them, which are a quarter of the set for that reason. A detector for nine digit numbers can be made to find every one of them by dropping the requirement that something in the text name it, and the price list is what stops that from looking like an improvement.

## The five defects this found

The measurement is worth having because of what it turned up, and all five were fixed in the detectors rather than by editing what was marked. Four came out of the first clean run and the fifth came out of the Windows leg of CI.

The address chain read `đường dây nóng` as a street, because `đường` opens a Vietnamese address and a hotline is literally a line. The chain then walked on through the phone number that followed, and `resolve` dropped the phone number for overlapping something bigger. One phrase in a short list of things that open like an address and are not one.

A tax code was filed as a phone number, because `điện thoại` sat earlier in the same sixty byte window and the phone branch happened to run first. Cues are now compared by distance and the nearest one wins, which is what a person reading the line does.

`Lê Hoàng Nam` came out as `Lê Hoàng`, because the list of syllables that cannot be part of a name held `Năm`, the word for year, and it was being matched with the tone marks off, where `Năm` and `Nam` are the same string. The words that are only excluded when they carry their marks are now a separate list matched with marks on.

`Chủ quán tên Trịnh Văn Đức` produced no name at all, because `quán` is one of the words that says a name belongs to a business rather than a person. The words that introduce a person are now checked first, so `chủ`, `tên`, `anh` and the rest beat the business test they were losing to.

The first clean run before those fixes reported 1.000 on every detector, which on a set the author wrote is a warning rather than a result. `rao-vat-ne-bo-loc.txt` was added afterwards for that reason.

The fifth came from the Windows leg of CI, which checks the fixtures out with `\r\n` endings, and it is the most serious of the five because nothing about it looks wrong. The co-occurrence scope for a name is a paragraph, a paragraph ends at a blank line, and a blank line was being read as two newline bytes in a row. Text with Windows endings has none of those, so the whole document became one paragraph, and one phone number anywhere on the page made every name on it a candidate. The motorbike advertisement gave up `Hà Nội` out of its headline and the job application gave up two words of its own title. Name precision on the set fell from 1.000 to 0.714, and it would have fallen on any Windows authored page in the crawl without anybody knowing. A blank line is now a line with nothing on it but space, and `TestTheLineEndingsDoNotChangeTheAnswer` measures the whole set both ways and requires the same numbers.

The fixtures are pinned to their committed line endings in `.gitattributes`, since a measurement that reports one recall on one platform and another somewhere else is not a measurement. The detectors are held to reading either ending by that test rather than by the pin, because the pin protects the fixtures and the crawl is not pinned to anything.

## What is not found, and why it stays that way

Three spans, each one a class rather than an accident, and `TestWhatTheDetectorsDoNotFind` fails the build if a fourth appears.

`quancafe.sangnhuong (a) gmail.com` is an email address written to get past a site's own filter, and that is how a large share of them are written in classified listings. Finding it means reading `(a)` and `[at]` and `chấm` and a dozen other spellings as an address, and every one of those rules costs precision on ordinary prose. It is left as a known gap rather than closed badly.

`031947265` is nine digits with nothing in the text naming them. The detector documentation already says an old national ID is only ever found by being named, because nine digits alone cannot be told from a product code or a price, and the price list in this set is the evidence for that. Closing this one would cost the precision that document measures.

`thôn Đông Trại, xã Nghĩa Trụ, huyện Văn Giang, Hưng Yên` is a rural address with no house number to open the chain. The chain starts at a number and walks outward, which is the rule that keeps it from swallowing sentences, and a rural address that starts at a hamlet has nothing for it to start from. This is the one of the three most worth fixing, and fixing it means letting the chain open on a unit word instead of a number, which needs its own measurement before anybody should trust it.

## What this does not tell you

Twelve documents and forty nine spans say the detectors work on the shapes somebody thought of. They say nothing about the shapes nobody thought of, and a redaction pass is judged on exactly those.

The number that matters for the corpus is measured on the fleet, against a sample drawn from a real crawl and read by hand after the fact. This set is what keeps the detectors from regressing between those runs, and it is the record of which cases were considered when the current trade between the two mistakes was chosen. It is not a substitute for measuring the crawl.
