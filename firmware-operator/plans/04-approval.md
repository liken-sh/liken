# 04, Approval

Proposed on 2026-10-20, as a stub. Not built. This plan lets a person
approve one version of a firmware device model for the machines that
have it. It applies nothing: [plan 05](05-applying-updates.md) applies
an approved version. [Plan 03](03-discovery.md) creates the
discovered `Firmware` objects that this plan approves.

## The approval

A person sets `spec.update.version` on a discovered `Firmware`, and
pins the cabinet from the release that `status.machines` lists:

```yaml
spec:
  update:
    guids: [9c13b7f1-d618-5d68-be61-6b17881014a7]
    version: 0.0.4.2
    cabinet:
      url: https://fwupd.org/downloads/9c085d03…-Framework_Laptop_13_RyzenAI300_fwupd_4.02.cab
      sha256: 9c085d03d6ea515101a8519f7855bf951e7693ff4f392585bbb692275a8b27f5
```

- `version` is the approval. No other field approves an update, and
  `Cluster` and `Machine` gain no field.
- `cabinet.sha256` pins the file the way a digest pins an image. The
  operator checks the cabinet against it before anything reads the
  file.
- `spec.machines`, a label selector, limits the approval to some
  machines, such as one machine first.
- The operator writes an `Approved` condition: `True` when a catalog
  offered this version with this SHA-256 to at least one machine that
  the approval covers.

## Questions for the full plan

- **Approval that the operator sets.** An option on the operator, or a
  field on a `FirmwareCatalog`, could approve each new release of chosen
  models on its own.
- **A model to leave alone.** A field that keeps a model out of
  proposals, for a device that a person never wants to update.
- **A version that a later catalog withdraws.** LVFS can remove a
  release. An approval of a removed release needs a reported state.
