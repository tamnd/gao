# The golden corpus

Six documents of real Vietnamese, each carrying the damage a real Vietnamese
document arrives with. The `.in` file is what came in and the `.out` file is what
`normalize.Normalize` is expected to produce. Neither is generated from the other by
hand: run `go test ./phoi -update` to rewrite the outputs, then read the diff
before you commit it.

Most of what is wrong with these files does not show up in a terminal, which is
the whole reason the stage exists. This is what is in each one.

**bao-dien-tu** is a news page scraped out of HTML. It starts with a byte order
mark, every line ends with a carriage return and a newline, the markup left four
spaces of indentation on each paragraph and a run of them in the middle of a
sentence, and there is a non-breaking space between the number and its unit.

**bach-khoa** is an encyclopedia article that went through a bad font conversion.
It writes the Icelandic eth where the Vietnamese đ belongs, carries a zero width
space inside the word Việt, and has a Cyrillic o in place of a Latin one, which
is the shape search engine cloaking takes.

**dien-dan** is a forum post typed with the input method off for part of it. The
keystrokes come out as they were typed and they stay that way: this stage counts
them and does not touch them, because deciding that `dduwowngj` was meant to be
đường is guessing, and the next word along the rule would guess wrong.

**ban-in** is text pulled out of a PDF. The typesetter broke a word across a line
with a soft hyphen, the line breaks are line separators rather than newlines, and
the punctuation is fullwidth because the document was edited on an input method
built for Chinese.

**dau-cu** is a page written throughout in the older of the two tone mark
conventions. Nothing in it is a spelling error and every syllable of it moves.

**font-cu** is a page from before Unicode. It is TCVN3, which is what the .VnTime
fonts draw, and it is stored here the way a crawler hands it over: the bytes have
already been read as windows-1252, so the file holds the mojibake anybody who
reads Vietnamese has seen a thousand times rather than the original bytes. The
line endings are carriage return and newline, which is the other half of what it
is here for, since the stage that settles those would eat a byte this one still
needs.
