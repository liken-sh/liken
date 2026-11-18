# The driver attribute can split a character

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`truncate` in `capabilities/devices.go` cuts the libva vendor string
at a byte offset, `s[:limit]`, before it becomes the driver attribute
of a published device. When a multi-byte UTF-8 character crosses the
limit, the attribute ends with part of the character, which is not
valid UTF-8.

## Evidence

```go
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
```

## What would confirm it

A test that calls `truncate` with a string whose character at the
limit takes two bytes, and checks the result with `utf8.ValidString`.
A second check is whether the API server refuses a `ResourceSlice`
whose string attribute is not valid UTF-8.

## What a fix has to decide

Whether to cut at the last character boundary at or below the limit,
and whether the limit counts bytes or characters, which is a question
of what the `ResourceSlice` validation counts.
