# Log lines that join two statements

Open problem, recorded on 2026-11-18. Checked against the code on
2026-11-19: a search of the Go files outside the tests for a format
string that holds `"; "` finds such lines, for example
`television_show.go:36`, `"%s; asked Television %s to show Display %s"`.

About 60 log strings join two statements with "; ", against the rule
in [`brand/voice.md`](../../../brand/voice.md), under Sentences, to
write two sentences in place of a semicolon. Some log lines also say that the operator "asked" a
television to do something, where the operator sent a command. The
rewrite that
[root plan 93](../../../plans/93-finish-the-plain-words-rewrite.md)
describes left both, because tests match many of the strings.

## What would confirm it

Search the Go files for `"; ` inside the string of a log call.

## What a fix has to decide

Whether the second statement becomes a key and a value of the same
line or a second line, and which tests match less of the text.
