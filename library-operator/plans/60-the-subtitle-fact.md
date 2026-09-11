# 60, The subtitle fact

A plan for later. Nothing here is built. The enricher gains a fact that
fetches the subtitles a video lacks in the household's languages, so
that no other tool writes beside the media for them. Syncing a subtitle
to the audio is a later plan.

## The problem

Subtitles are the largest enrichment another tool still owns. A
subtitle downloader watches the volume, asks OpenSubtitles for each
video that lacks a text track in a wanted language, and writes the
file beside the video. Every other file beside the media is this
operator's now. The catalog already classifies a subtitle file and
reads its language off its name, so the gap is known; nothing fills
it.

## The design

**The languages.** The household's languages already exist: the
default `MediaPreferences` of the media-operator names
`subtitleLanguages`, and this operator reads that resource today. The
fact takes its list from there, so a house states its languages once.
A cluster with no `MediaPreferences` has no subtitle gap.

**The gap.** A video is in the gap for a language when it has no
embedded text subtitle stream in that language and no subtitle file
beside it in that language, whoever wrote the file. The probe's stream
rows say the first; the scanner's subtitle rows say the second. A
forced-only embedded track does not count as a track in the language.
An image subtitle stream, such as PGS, does not count either, because
a screen reader and a search cannot read it; that ruling is open to
change after the first drill.

**The provider.** OpenSubtitles, through its REST API, as a
`MetadataProvider` of a new kind, with the API key and the account in
a `Secret` the way the other providers hold theirs. A download needs
an account, and an account has a daily download cap. The cap is a
first-class limit of the fact: the container reads the remaining
count from each answer, stops at zero, records nothing for the videos
it did not reach, and the next run carries on. A run never spends the
cap on the same video twice, because a fetched file closes the gap.

**The search.** By the file's hash and size first, which OpenSubtitles
matches exactly, then by the title's provider id and, for an episode,
the season and episode numbers. The best-rated match in the language
wins. A search that finds nothing is a miss with a date, and the retry
window applies, the way an art fact with no image retries.

**The name.** `<video base>.<language>.srt`, with `.hi` before the
extension for a hearing-impaired track and `.forced` for a forced one,
which is the form Jellyfin and Kodi read and the scanner already
parses. The write goes through the write package: a temporary and one
rename, and never over a file that exists.

**Two writers.** While a subtitle downloader still runs beside this
fact, both fetch for the same gap. The file that lands first closes
the gap for the other, because each checks the volume before it
fetches, so the worst case is one wasted download. The guide says to
turn the downloader off once the fact runs.

**What is left out.** Syncing a subtitle to the audio, which needs the
audio decoded and a tool this image does not hold. Translating a
subtitle. Upgrading a subtitle a person or the downloader already
placed.

## How the work is proved

Table tests cover the gap rule for every combination of embedded
track, file beside, forced, and image subtitles. A fake OpenSubtitles
server proves the hash search, the fallback search, the name written,
the cap reached mid-run, and the miss. The drill runs on `liken-1`
against a copy of a few titles with their subtitle files stripped, and
counts the files the fact wrote and the downloads it spent.
