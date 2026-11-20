# Testing against vivid

The kernel's
[`vivid`](https://docs.kernel.org/admin-guide/media/vivid.html#cec-consumer-electronics-control)
driver emulates a TV with HDMI inputs, and sources on HDMI outputs.
Each of them has a real kernel CEC adapter, and all of the adapters
are on one bus. The `Vivid` tests in this package and in the operator
check the code against the kernel's own CEC core, so `cectest` cannot
replace them.

You run them by hand, on a Linux machine with the `vivid` module and
`v4l-utils` installed. They skip when the user cannot open a vivid
adapter, which is the case in CI. To run them:

```sh
# Three HDMI inputs and three HDMI outputs. Each output's adapter is
# on the TV's bus.
sudo modprobe vivid num_inputs=3 input_types=0x3f num_outputs=3 output_types=0x07

# An output has a physical address only while an input shows it.
sudo v4l2-ctl -d /dev/video2 -c hdmi_000_0_is_connected_to=2 \
  -c hdmi_000_1_is_connected_to=3 -c hdmi_000_2_is_connected_to=4

# The nodes are root:video 0660.
sudo chmod 666 /dev/cec*

go test -run Vivid -v ./cec/ .
sudo modprobe -r vivid
```

`/dev/video2` is the capture node named `vivid-000-vid-cap` under
`/sys/devices/platform/vivid.0/video4linux`. Its number depends on
the other video devices on the machine.

A vivid output adapter gets its physical address from its port. It
rejects `CEC_ADAP_S_PHYS_ADDR` with `ENOTTY`, the same as any adapter
on a video port. `cectest` does the same for an adapter without
`CapPhysAddr`. The node workload then announces the port's own
address and says so in its entry.

The monitor test needs `CAP_NET_ADMIN`, so an ordinary run skips it.
To include it, build the test binary with `go test -c ./cec/` and run
the binary with `sudo`.

When vivid's TV adapter has no follower, the kernel's CEC core
replies to Give Device Power Status with a Feature Abort, both before
and after an Image View On. So a program must act as the TV. Each
`Vivid` test acts as the TV itself, on the capture adapter.
`TestVividTheWakeShowsTheDisplay` also acts as a streaming player on
a second output. That player claims Active Source once, after the
node workload's claim. The test skips when only one output is
connected.

`TestVividTheNodeWorkloadWakesTheTV` can use `cec-follower` from
`v4l-utils` as the TV instead. `cec-follower` is an independent
implementation of a TV's power states. After Image View On, it
reports Standby for about 2 seconds, and then ToOn for about 6 more.
Start it, and then run that test alone, because the other `Vivid`
tests claim the same adapter:

```sh
# /dev/cec0 is the adapter named vivid-000-vid-cap0.
cec-ctl -d0 --tv -o TV >/dev/null
cec-follower -d0 -s -n &

CEC_VIVID_TV=cec-follower go test -run TestVividTheNodeWorkloadWakesTheTV -v .
kill %1
```
