# A change to the closure library

`vulkan/closure.sh` is the closure library. Every closure on a Debian
or Ubuntu builder sources it: the bases, `astrometry`, and the
closures of `audio-operator` and `git-csi-driver`, which use the
`vulkan` directory as the build context `builder`. So a change to the
library needs a new revision of `vulkan`, and the cascade in section
5 of `SKILL.md`.

`indi` has a copy, `indi/closure.sh`, and `ci pins` fails when the
copy differs. So copy the change into it, and raise the revision of
`indi` too.

The library writes the notices of each image, its list of packages,
and its dpkg records under `/var/lib/dpkg/status.d/`, which syft and
Trivy read. "Closures" in the `images` skill describes the form.
