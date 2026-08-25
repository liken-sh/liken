A `Player` is one named unit of equipment: a lone speaker, a TV
with its built-in speakers, a TV with a receiver. The spec selects
the unit's devices out of what the hardware operators publish, with
the same CEL selectors a hand-written `ResourceClaim` would use.
Between runs, the operator holds one claim on the unit's display
for the idle screen. It claims the other devices only while a
[Play](/docs/reference/plays/) runs on it.

The resource is namespaced, and everything a `Player` becomes is
created in its namespace: the claims, the playback pod, and the
`Play` that names it, so RBAC on the namespace covers the set.

    apiVersion: media.liken.sh/v1alpha1
    kind: Player
    metadata:
      name: studio
      namespace: media
    spec:
      zone: studio
      displayName: Studio Lab
      display:
        class: display
        displayName: Portable Screen
      render:
        class: gpu-render
      sinks:
        - class: audio-output
          displayName: Built-in Speakers
      remotes:
        - name: studio-gamepad
          displayName: Studio Controller
      idle:
        fadeAfterSeconds: 600

The class names here are the cluster's own vocabulary: consumer
`DeviceClass` objects are yours to create, and each hardware
operator's manual gives the YAML for its class.
