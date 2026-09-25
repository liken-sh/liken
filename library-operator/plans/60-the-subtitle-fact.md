# 60, A fact that fetches subtitles

This is a plan for later. Nothing here is built. The enricher gets a new
fact that fetches the subtitles a video lacks in the household's
languages. Then no other tool needs to write subtitle files beside the
media. Syncing a subtitle
to the audio is a later plan.

## The problem

Subtitles are the largest enrichment that another tool still does. A
subtitle downloader watches the volume, queries OpenSubtitles for each
video that lacks a text track in a wanted language, and writes the
file beside the video. This operator now writes every other file beside
the media. The catalog already classifies a subtitle file and reads its
language from its name, so the catalog already records the gap. Nothing
fills it.

## The design

**The languages.** The household's languages already exist: the
default `MediaPreferences` of the media-operator has
`subtitleLanguages`, and this operator reads that resource today. The
fact takes its list from there, so a household states its languages
once.
A cluster with no `MediaPreferences` has no subtitle gap.

**The gap.** A video is in the gap for a language when it has no
embedded text subtitle stream in that language and no subtitle file
beside it in that language, whoever wrote the file. The probe's stream
rows record the first. The scanner's subtitle rows record the second. A
forced-only embedded track does not count as a track in the language.
An image subtitle stream, such as PGS, does not count either, because
a screen reader and a search cannot read it. This decision can change
after the first drill.

**The provider.** OpenSubtitles, through its REST API, as a
`MetadataProvider` of a new kind. The API key and the account are in a
`Secret`, as for the other providers. A download needs an account, and
an account has a daily download cap. The fact handles the cap directly.
The container reads the remaining count from each response and stops at
zero. It records nothing for the videos it did not reach, and the next
run continues from there. A run never uses the cap twice for the same
video, because a fetched file closes the gap.

**The search.** The fact searches first by the file's hash and size,
which OpenSubtitles matches exactly. Then it searches by the title's
provider id and, for an episode, the season and episode numbers. The
fact takes the best-rated match in the language. A search that finds
nothing records a miss with a date. The retry window then applies, the
same way an art fact with no image retries.

**The name.** `<video base>.<language>.srt`, with `.hi` before the
extension for a hearing-impaired track and `.forced` for a forced one.
Jellyfin and Kodi read this form, and the scanner already parses it.
The fact writes through the write package: it writes a temporary file
and does one rename. It never writes over a file that exists.

**Two writers.** While a subtitle downloader still runs beside this
fact, both fetch for the same gap. Each checks the volume before it
fetches, so the first file written closes the gap for the other. The
worst case is one wasted download. The guide says to
turn the downloader off once the fact runs.

**What is left out.** Syncing a subtitle to the audio, which needs the
audio decoded and a tool that this image does not include. Translating a
subtitle. Upgrading a subtitle a person or the downloader already
placed.

## How the work is tested

Table tests cover the gap rule for every combination of embedded
track, file beside the video, forced, and image subtitles. A fake
OpenSubtitles server tests the hash search, the fallback search, the
name written, the cap reached mid-run, and the miss. The drill runs on `liken-1`
against a copy of a few titles with their subtitle files stripped, and
counts the files the fact wrote and the downloads it used.
