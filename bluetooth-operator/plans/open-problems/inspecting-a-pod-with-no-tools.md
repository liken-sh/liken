# Inspecting a pod with no tools

Open problem. The `bluetoothd` image is `FROM scratch` and carries
four binaries: `bluetoothd`, `bluetoothctl`, `dbus-daemon`, and the Go
entrypoint that starts them. There is no shell and no coreutils, so
the ordinary way to look at a pod's state does not run here. Nobody
has decided what replaces it.

## What happened

The hardware drill on 2026-08-17 replaced the operator's pod and had
to confirm that the bonds on the `bonds` volume survived. The obvious
check was a directory listing:

    kubectl exec -n liken-system bluetooth-operator-0 -c bluetoothd \
      -- ls /var/lib/bluetooth

It returned `executable file not found in $PATH`. There is no `ls` in
the image, and there is no shell to report a better error.

The drill used `bluetoothctl` instead, and got a partial answer. The
prompt read `[DualSense Wireless Controller]>`, which proves that
bluetoothd holds a device object for the controller. That object came
from the bond files, because no discovery scan ran in that session.
So the bonds did survive, and the drill recorded that.

The full answer was still out of reach. `bluetoothctl` renders its
command output only on a real terminal, so `devices Paired` printed
nothing that `kubectl exec` could capture. The evidence was the
prompt, not a listing.

## The question

What is the recipe for reading this pod's state, now that the image
carries no general-purpose tools? Somebody debugging a bond, a
permission, or a missing file needs an answer that works the first
time they reach for it, and the README does not have one.

## The candidates

Three, and nobody has picked one.

- **A debug container.** `kubectl debug` attaches an ephemeral
  container from any image, and `--target` shares the target's process
  namespace. A busybox or a distroless-debug image would bring the
  shell and the coreutils that the pod does not carry. The open part
  is the volume: an ephemeral container gets the pod's volumes only if
  its spec declares the mounts, and whether that reads the `bonds`
  volume in practice has not been tested.
- **A status surface on the operator.** The operator already reads
  bluetoothd over D-Bus and already holds the paired set, because it
  writes that set into a ResourceSlice. A read-only endpoint, or more
  fields on the slice, would answer most of these questions with no
  exec at all. That is new code and a new surface to keep correct.
- **Accept `bluetoothctl` and write down what works.** Record the
  exact invocations that produce readable output through
  `kubectl exec`, and record which ones do not, so nobody rediscovers
  the terminal behavior. This costs nothing but leaves the filesystem
  unreadable.

The three are not exclusive. The last one is worth doing whatever else
happens, because the drill spent time on a question that a documented
invocation would have answered.
