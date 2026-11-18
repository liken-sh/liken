# Two comments give different reasons for the WirePlumber fragment order

Open question, recorded on 2026-11-18. Nothing fails today. WirePlumber
0.5's rule for the order of its fragments was not checked.

The declare init container writes `60-liken-bluetooth.conf` into
`/etc/wireplumber/wireplumber.conf.d`. The fragment turns WirePlumber's
Bluetooth monitor back on, so it must be read after the image's
`50-audio-operator.conf`, which turns it off. The image copies that
file and `51-access-rules.conf` into
`/usr/share/wireplumber/wireplumber.conf.d`.

`audio-operator/bluez.go` gives two reasons why the fragment is read
last:

- The comment on `wireplumberConfigDir` says that WirePlumber reads the
  fragments in `/usr/share` before the fragments in `/etc`.
- The comment on `monitorDropInName` says that the file name starts
  with 60 so that it sorts after the image's fragments, which start
  with 50 and 51.

With the files as they are, both rules give the same order. They give
different orders when a fragment in `/usr/share` sorts after 60, or
when the generated fragment moves to `/usr/share`. A reader cannot
tell which rule to rely on.

## What would confirm it

WirePlumber 0.5's source, where it collects the files of each
`wireplumber.conf.d` directory, or a test in the WirePlumber image
with a fragment in `/usr/share` named `70-` that sets the same key.

## What a fix has to decide

Only the comments change: they state the rule that WirePlumber
applies, and the file name keeps its prefix when the prefix is part
of that rule.
