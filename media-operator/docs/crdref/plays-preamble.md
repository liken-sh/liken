A `Play` is one run of media on a [Player](/docs/reference/players/):
a film, an album, or a season of episodes, played in order. Its
lifecycle is analogous to a `Job`'s: it runs once to completion, and
it stays for its status until `ttlSecondsAfterFinished` passes or a
person deletes it. Create a `Play` to start it, delete it to stop it
early, and `kubectl get plays` lists what plays right now.

The operator reconciles a `Play` into one playback pod and the
claims that pod needs, all owned by the `Play`, so deleting the
`Play` is the whole teardown: the garbage collector takes the pod
and the claims with it. A `Finished` run leaves nothing running.

The spec is immutable, like a `Job`'s template. A `Play` whose
player or media changed mid-run would describe a different run;
delete the `Play` and create another.

One `Player` runs one `Play`. When two unfinished `Play`s name the
same `Player`, the newest one by creation time, and then by name, is
the one that runs, and the operator deletes every older one. So a
`Play` created while a film plays ends that film. The deleted `Play`
reports its last position before its pod ends, and a `Play` that
resumes it names that position in `spec.start`.

`spec.next` names the work that follows this run. The display offers
it on the scrubber, and when a person takes the offer the program that
wrote the `Play` creates the next one. The `Play` carries the offer
because the display reads no catalog, and the program that wrote it
decides what follows.

    apiVersion: media.liken.sh/v1alpha1
    kind: Play
    metadata:
      name: dune
      namespace: media
    spec:
      players: [studio]
      items:
        - uri: nfs://nas/media/movies/Dune (2021)/Dune.mkv
          presentation:
            type: video
            hint: movie
            title: Dune
            year: 2021
      start: "0:10:00"
