# The compositor image

Plan 01. Built. The compositor ships as a library closure on scratch,
the pod runs one container, and two images publish from one
`Dockerfile`.

## The problem

Debian ships every libweston backend in one package. `rdp-backend.so`,
`vnc-backend.so`, and `remoting-plugin.so` sit beside
`drm-backend.so`, so installing `weston` also installs FreeRDP,
neatvnc, GStreamer, PipeWire, libavcodec, libx265, and a speech
synthesiser.

This operator loads four modules: the DRM backend, the headless
backend, the GL renderer, and the kiosk shell. Package-level trimming
cannot express that. There is no package that holds those four and not
the other eight, and removing the package removes all twelve.

## The design

`weston-closure.sh` takes the four modules and the two programs the
image needs, resolves what the loader needs for each, and copies that
tree into a directory. The image is `FROM scratch` with that tree
copied in. It has no shell, no package manager, and no libc userland
beyond the libraries the closure resolved.

The script copies every hop of a symlink chain and then the file at the
end of it, because the loader opens a library by its soname and a
soname is a link. It writes `/etc/ld.so.conf` naming the multiarch
directory and runs `ldconfig -r`, because without a cache the loader
searches its built-in directory list, and that list does not name the
directory these libraries live in.

The loads that `ldd` cannot report were added by hand, and they are the
subject of
[an open problem](open-problems/loads-that-ldd-cannot-see.md).

## What it measured

The deployed image went from 607,683,315 bytes to 251,907,734 bytes.
That is a reduction of 58.5%.

The compositor image alone is 234,346,900 bytes. The operator's binary
is the remaining 17 MB.

## What stays and why

* **`libLLVM.so.19.1` at 127 MB and `libgallium` at 42 MB.**
  `libEGL_mesa` names both as `DT_NEEDED`, so the loader resolves them
  before the compositor runs a single frame. Together they are 169 MB
  of the 234 MB image. That is the floor for a GL renderer on mesa, and
  no closure can go under it.
* **Every DRI driver.** The closure names fourteen files in the `dri`
  directory. Thirteen of them are symlinks and the fourteenth is
  `libdril_dri.so`, the one real driver behind all of them. So the
  image runs on any card mesa supports, for the cost of one file and
  its links, rather than on the card it was built for.
* **`wayland-info`.** It lists every global the compositor advertises,
  which is the first thing to read when a client connects and draws
  nothing. `kubectl exec` runs a binary by name and needs no shell to
  do it, so in an image with no shell it is the only diagnostic
  available.

## One container, not two

The pod keeps one container. Splitting the compositor and the operator
into two containers was considered and rejected.

The split saves no bytes. The closure is 234 MB either way, and the
operator's binary is 17 MB whether it sits in its own scratch image or
as one more layer on the compositor's.

The split costs the supervision contract. The operator writes
`weston.ini` from the outputs it enumerated and then runs the
compositor as its own child, so a compositor that exits ends the
operator, and the kubelet restarts both. Nothing in a pod spec binds
one container's life to another's. Rebuilding that binding across a
shared volume needs three things that do not exist today: a death
signal written from scratch, a bounded wait for the configuration file
in a compositor entrypoint, and two independent kubelet restart loops
that agree on which generation of the configuration is live. That is
three new silent failure modes in place of one exit status.

## Two images, one file

`ghcr.io/liken-sh/weston` is the compositor image and
`ghcr.io/liken-sh/display-operator` is that image plus the binary.

The compositor image publishes on its own because it is the artifact
the release starts and reads the log of. The operator image is built
`FROM` it, so the compositor that passed the check and the compositor
the pod runs are the same set of bytes and cannot drift. The two share
every layer, so a node that pulls both pulls the second for the size of
one binary.

## What was proven, and where

Two runs, both local, neither one on a target machine.

Headless, with no graphics card:

* the headless backend loaded the GL renderer,
* EGL 1.5 on Mesa,
* `GL renderer: llvmpipe (LLVM 19.1.7)`,
* GL ES 3.2,
* the output enabled,
* the kiosk shell loaded.

With a real card and the DRM backend:

* libseat opened with the `noop` backend,
* `DRM: supports atomic modesetting`,
* `GL renderer: Mesa Intel(R) Arc(tm) Graphics (MTL)`,
* an EDID read off `eDP-1`,
* `Output 'eDP-1' enabled` at 2256x1504@60.

The second run used a laptop's card. GPU and DRM on the target machines
is a separate proof, and a laptop is not one of those machines.
Milestone 57 states what a drill on `liken-1` with two monitors must
show, and none of that has run.
