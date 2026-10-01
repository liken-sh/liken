# 13, The receiver's whole setup

Proposed on 2026-10-01. A Denon or Marantz `Receiver` reports and
accepts every setting the receiver exposes on the network, not only the
six families it covers now. The speaker setup is the gap that matters
most: a person setting up a liken home theater needs to read which
speakers the receiver drives and how it calibrated them, and today has
to walk to the receiver and open its on-screen menu.

## The problem

`spec.denon.settings` and `status.denon` cover `audio`, `audyssey`,
`channelVolumes`, `hdmi`, `system`, and `tone`. The receiver's speaker
setup is missing: which speakers are configured and at what size, the
amp assignment, the distances, the crossovers, the calibrated levels,
and the subwoofer mode. So are input names and visibility, quick
select names, and zone 2's setup.

This showed during the speaker walk of audio-operator's plan 12 on a
home cluster. The walk's side and back windows both played on the
surround speakers, and finding out why took a trip into the receiver's
on-screen menu. The receiver was set up as 5.1 with two height
speakers, and its height pair was assigned as Top Middle. A `Receiver`
status that showed the speaker setup would have answered both questions
in one `kubectl get`.

## What the receiver exposes

The receiver has two network interfaces, and neither covers
everything.

| Family | Control port (TCP 23) | HTTP |
|---|---|---|
| Speaker config and size | `SSSPC` read and set, undocumented | none found |
| Amp assign | none | the HTTPS setup interface on port 10443, on models that serve it |
| Distances | `SSSDE`, undocumented | none |
| Crossovers | `SSCFR`, undocumented | none |
| Calibrated levels | `SSLEV`, undocumented, read | none |
| Subwoofer mode | `SSSWM`, undocumented | none |
| Sub, LFE, DRC, dialog, delay | `PS…`, documented | none needed |
| Input names and visibility | `SSFUN`, `SSSOD`, undocumented, read | `GetSourceRename`, `GetDeletedSource`, `SetSourceRename` on `AppCommand0300.xml`, undocumented |
| Quick select names | `SSQSNZMA`, undocumented | `GetQuickSelectName`, undocumented |
| Zone 2 | `Z2…`, documented | none needed |
| HDMI CEC, ARC, power-off control | none to read; CEC toggles only by a remote-key command | port 10443, on models that serve it |

"Documented" means Denon's published control protocol: the operator
cites the CY2022 version, because the version that covered the
X1700H's year is no longer on Denon's site. "Undocumented" means the
commands the Home Assistant `denonavr` library and other community code
send. A receiver tested on a home cluster, an X1700H, refuses
connections on port 10443, so on that model amp assign and the HDMI
setup menu have no network path.

## The design

### Read first

Every family the receiver answers goes into `status.denon`, read with
the queries above at connect and kept current from the receiver's own
event lines, the way the six families are read now. Reading is safe, so
it comes first and covers every family, documented or not.

A family the receiver does not answer is left out of status, and
`status.denon.unsupported` lists it, so a person can tell "not
supported on this model" from "not read yet". The operator learns that
from the receiver, not from a table of models.

### Then set

The spec gains a block for each family that the receiver accepts
writes for: `speakers` (configuration, sizes, distances, crossovers,
subwoofer mode), `inputs` (names and visibility), `quickSelect` (names),
and `zone2`. Each block works like the existing families: the operator
compares each declared field with what the receiver reports, sends only
a field that differs, tries a value at most three times, and names a
field that never takes in the `SettingsConfirmed` condition. An omitted
key leaves the receiver as it is.

An undocumented command is only declarable after a drill shows that the
receiver reads it back at the value it was sent. A family that fails
that drill stays read-only.

### The HTTP interface

The operator talks to the HTTP interface only for a family the control
port cannot reach, and only on a receiver that serves it: amp assign and
the HDMI setup menu on port 10443. The receiver's description document,
which the operator already fetches, names the model, and a connection
refused on 10443 marks those families unsupported for that receiver.
`/goform/AppCommand0300.xml` adds nothing the control port lacks except
input renaming, so the operator uses the control port for that too where
it can.

### Asking the receiver directly

The `Receiver`'s commands topic gains one diagnostic command,
`query`, that sends one read-only query line, a line ending in `?`, and
logs the receiver's answer. Today the control port is the operator's
alone, and Denon's documents and community code disagree on whether the
receiver accepts a second connection. The query command lets a person,
or the drill below, read any setting with no second connection and no
risk of disturbing the operator's own. It refuses any line that does
not end in `?`, so it can never change a setting.

## Considered and set aside

* **A second control connection for diagnostics.** Whether the
  receiver accepts it, refuses it, or drops the operator's connection is
  not settled, and the operator's connection carries the receiver's
  event stream.
* **A table of models and the families each supports.** The receiver
  answers or does not, and a table would go stale with each model year.
* **HTTP for everything.** `AppCommand0300.xml` covers less than the
  control port, and its commands come from a packet capture of Denon's
  phone app.
* **Raw write commands on the commands topic.** A setting belongs in
  the spec, where it is declared, compared, and confirmed.

## How it will be proved

On a home cluster with an X1700H:

* `kubectl get receiver -o yaml` shows the speaker configuration,
  distances, crossovers, calibrated levels, and subwoofer mode, and they
  match the receiver's own setup menu.
* The `query` command answers `SSSPC ?` with the receiver's reply in
  the operator's log, and refuses a line with no `?`.
* A changed distance in `spec.denon.settings.speakers` reaches the
  receiver, the receiver's menu shows it, and the `Receiver` reports it.
* `status.denon.unsupported` lists amp assign and the HDMI setup menu,
  because the receiver refuses port 10443.
