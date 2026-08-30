# Richer file facts

A `file` row carries its container, its video and audio codec, its
resolution, its size in bytes, and a duration. The resolution and the
duration come from the `.nfo` streamdetails where a sidecar wrote them,
and from the file name where none did. The scanner reads no media file
itself, because its image carries no probe.

A future enhancement reads the file's own container metadata for the
facts a sidecar and a name cannot give: a duration measured from the
container rather than guessed, the bitrate, the HDR format, the audio
channel layout, and the quality settings of the encode. This needs a
media-probe capability in the scanner, which the current design leaves
out on purpose, so it is deferred and belongs with the enrichment work
in plan 11.
