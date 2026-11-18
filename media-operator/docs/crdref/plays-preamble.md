A `Play` is one run of media on a [Player](/docs/reference/players/):
a film, an album, or a season of episodes, played in order. It works
like a `Job`: it runs once to completion, and then it stays, so that
you can read its status, until `ttlSecondsAfterFinished` passes or you
delete it. Create a `Play` to start it, and delete it to stop it early.
`kubectl get plays` shows the status, item, and position of each run,
finished runs included.

The operator creates a playback pod for a `Play`, and the claims that
the pod needs. The `Play` owns the pod and the claims, so the
Kubernetes garbage collector deletes them with the `Play`. A
`Finished` `Play` has no pod running.

The operator adds the finalizer `media.liken.sh/bus-topics` to every
`Play`, so a deleted `Play` stays in the API server until the operator
has stopped the run. The operator deletes the playback pod, waits
until the pod is gone, and then clears the run's retained `status` and
`availability` topics on the bus. It waits because the pod publishes
closing messages as it ends, and those would overwrite an earlier
clear. When a `Play` stays in deletion for more than a few seconds,
its pod does not stop. Run `kubectl describe` on the pod to see why.

The spec is immutable, like the template of a `Job`, because a changed
`Play` would no longer describe the run that is playing. To change the
player or the media, delete the `Play` and create another.

A `Player` runs one `Play` at a time. When two unfinished `Play`
objects name the same `Player`, the newest one runs, by creation time
and then by name, and the operator deletes every older one. So a
`Play` that you create while a film plays ends that film. The deleted
`Play` reports its last position before its pod ends. To resume from
that position, create a `Play` with the position in `spec.start`.

`spec.next` describes the work that follows this run, and the
on-screen display offers it on the scrubber. When a person accepts the
offer, the program that wrote the `Play` creates the next one. The
offer is in the `Play` because the program that created the `Play`
decides what follows, and the on-screen display reads no catalog.

    apiVersion: media.liken.sh/v1alpha1
    kind: Play
    metadata:
      name: nosferatu
      namespace: media
    spec:
      players: [studio]
      items:
        - uri: nfs://nas/media/movies/Nosferatu (1922)/Nosferatu.mkv
          presentation:
            type: video
            hint: movie
            title: Nosferatu
            year: 1922
      start: "0:10:00"
