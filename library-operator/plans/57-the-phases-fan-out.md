# The phases fan out

Plan 57. A stub from the 2026-09-11 split of [plan
34](completed/34-every-fact-writes-its-rows.md), whose row rules are
built and whose fan-out is not. The enricher runs probe, arrival,
identity, nfo, art, contributors, and trickplay as init containers in a
row. There is no `phases` volume and no mark file. This plan lets the
phases that do not share a file run at the same time.

## The problem

Plan 30 drew the art, contributors, and trickplay phases as regular
containers running at once, and built them as init containers in a
row, because the enrich container must run last and nothing told it
when a regular container beside it was done. Plan 34 made every fact
write its own rows, so a phase no longer waits on the next walk for
what another phase wrote. The order stands only because nothing marks
a phase as done.

## The phases

```yaml
kind: Job
spec:
  template:
    spec:
      initContainers:
        - name: corrosion            # native sidecar, restartPolicy: Always
        - name: probe                # writes streamdetails into the sidecar
        - name: identity             # writes the ids into the sidecar
      containers:
        - name: nfo                  # edits the sidecar body; no other container does
        - name: art                  # needs the ids; nothing else
        - name: contributors         # needs the people nfo's credits fact creates
        - name: trickplay            # needs the probe; nothing else
        - name: enrich               # the closer: waits for the marks, writes the runs row
      volumes:
        - name: phases               # emptyDir, one mark per finished phase
```

Probe and identity stay in a row, before everything, because the ids
they write are what every other phase asks a provider with. The nfo
phase is the only writer of the sidecar body, so it runs alone on that
file and beside everything else.

A phase runs its gap loop until the loop finds nothing and every phase
it depends on is done, then writes its mark, a file named for the
phase on the shared `emptyDir`. Art and trickplay depend on nothing
in the fan-out, so they run once. Contributors depends on nfo: it
loops, and each pass finds the people the credits fact has created
since the last pass, because those rows are already in the pod's own
catalog. It stops when nfo's mark is there and a pass finds nothing.

The closer waits for every mark the Job named in its environment, then
writes the runs row and waits for the echo, as today. The marks are
files and not rows because a file on the pod is instant and needs no
poll of the catalog.

## Proof

On `liken-1`, against the lab libraries. A run's `runs` row starts at
probe and ends after the last phase's mark, and the enrich Job's
containers show nfo, art, contributors, and trickplay running at once.

## What is not decided

Whether a phase's gap loop needs a floor between passes when its
upstream is slow. The contributors phase polling an empty gap every
second while nfo works through a thousand titles is cheap but
pointless; a short sleep between empty passes is the likely answer.
