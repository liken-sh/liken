A `Player` is one unit of equipment that plays media, such as a lone
speaker, a TV with its built-in speakers, or a TV with a receiver. Its
spec selects the `Player`'s devices from the devices that the hardware
operators publish, with the same CEL selectors that you would write in
a `ResourceClaim`. For the idle screen, the operator keeps a claim on
the shared draw device of the `Player`'s display, and on the render node
when `spec.render` is set. It claims the other devices only while a
[`Play`](/docs/reference/plays/) runs on the `Player`.

A `Player` name is at most 32 characters, because `media-operator` and
`library-operator` build the names of the objects they create from it,
and Kubernetes limits some of those names to 63 characters.

A `Player` is namespaced. The operator creates its claims and pods in
the `Player`'s namespace, and a `Play` refers to a `Player` in its own
namespace. So RBAC on the namespace covers all of them.

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

The `class` values are names of `DeviceClass` objects. You create the
consumer `DeviceClass` objects yourself, and the manual of each
hardware operator gives the YAML for its class.
