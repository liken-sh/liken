# Running the enricher phases in parallel

Plan 57. This is a stub from the 2026-09-11 split of [plan
34](completed/34-every-fact-writes-its-rows.md). Plan 34's row rules are
built, and its parallel phases are not. The enricher runs probe, arrival,
identity, nfo, art, contributors, and trickplay as init containers, one
after another. There is no `phases` volume and no mark file. This plan lets the
phases that do not share a file run at the same time.

## The problem

Plan 30 designed the art, contributors, and trickplay phases as regular
containers that run at the same time. It built them as sequential init
containers instead, because the enrich container must run last and the
Job had no completion mark for a regular container that runs beside it. Plan 34
made every fact write its own rows, so a phase no longer waits for the
next walk to see what another phase wrote. The phases still run in
order because nothing marks a phase as complete.

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

Probe and identity stay sequential, before all other phases, because
every other phase uses the ids they write to query a provider. The nfo
phase is the only writer of the sidecar body. No other phase writes that
file, so nfo runs at the same time as the other phases.

Each phase runs a loop that queries its gaps. The loop runs until it
finds no gaps and every phase that it depends on is done. The phase then
writes its mark, a file named for the phase on the shared `emptyDir`. Art and trickplay have no dependencies
in the fan-out, so each runs once. Contributors depends on nfo. It
loops, and each pass finds the people that the credits fact created
since the previous pass because those rows are already in the pod's
own catalog. It stops when nfo's mark exists and a pass finds nothing.

The enrich container finishes last. It waits for every mark that the Job
names in its environment. Then it writes the runs row and hands off to a
catalog pod, as it does today. The marks are files on the pod and not catalog rows, because a file is
visible at once and needs no poll of the catalog.

## Proof

On `liken-1`, against the lab libraries. A run's `runs` row starts at
probe and ends after the last phase's mark, and the enrich Job's
containers show nfo, art, contributors, and trickplay running at once.

## What is not decided

Whether a phase's loop needs a minimum interval between passes when
the phase it depends on is slow. While nfo works through a thousand
titles, the contributors phase polls an empty gap query every second.
This costs little, but it does nothing useful. A short sleep between
empty passes is the likely fix.
