# The boot does not wait for radios

Milestone 64. Interface bring-up runs in two passes: wired first,
then wireless in the background. The boot goes on the moment a
settled interface gives a route toward the cluster's endpoint.

## The problem

`bringUp` (`init/network.go`) raises the declared interfaces one at a
time, in line, in the boot path. Every interface must settle before
k3s starts. On 2026-08-26 that ordering took `liken-1` down: the
`rtw88` driver deadlocked the kernel inside `netlink.LinkSetUp`, the
boot stopped at `liken: bringing up wlan0`, and the machine never
reached k3s. The staged manifest still declared the radio, so every
following boot wedged at the same line. Only a reinstall ended it.

The wedge itself is a driver bug with its own fix (plan 62 records
it). The boot design made it unrecoverable. A machine with a healthy
wired path was destroyed by an interface it did not need, and nothing
on the machine could ever act again, because the wedge fired before
anything that could act existed.

A timeout around the raise cannot contain this class of failure. The
raise holds the kernel's `rtnl` lock while the driver runs, a wedged
driver holds it forever, and the stuck thread is unkillable. What the
boot can control is what it risks before the machine can act, and
what it refuses to wait for.

## The two passes

Pass one raises and addresses every wired interface, in spec order,
exactly as today. Wired raises return in milliseconds and their
addressing already bounds itself.

After pass one, init asks the question `parkDecision` already asks:
does any settled interface give a route toward the cluster's
endpoint? A machine alone, with no endpoint address to check, counts
as routed, which is the plan 62 bias restated: boot rather than wait.
If the answer is yes, the boot goes on. k3s starts, the operator
runs, and the machine can act.

Pass two runs the wireless bring-up for each declared radio in the
background. Nothing after the network step waits on it. The radio's
verdict lands in the facts tree when it settles, the same fields it
writes today, and the operator's watch carries it into the Machine's
status. A radio that joins late gets its address late and appears in
status late, and both of those beat a boot that waited.

If the answer after pass one is no, the machine's only declared path
is a radio, and pass two runs in the foreground as the bring-up does
today: join, address, and the park for a deterministic failure. The
park rule does not change. A machine that cannot reach its cluster
has nothing to do anyway, and this is the one machine the radio is
allowed to hold.

## The raise deadline

Pass two wraps each radio's `LinkSetUp` in a goroutine and stops
waiting after a deadline. A raise that has not returned is not an
association problem; it is a kernel thread that did not come back,
and no amount of waiting fixes it. Init reports it as its own
wireless state, distinct from `NoCarrier`, with a message that names
the stuck call. The goroutine is abandoned, not cancelled, because
nothing can cancel it; the report is the whole remedy.

The deadline exists for the report, not for recovery. After a wedge,
new network configuration on the machine blocks behind the held
`rtnl` lock, so pods that need new interfaces stop starting while
running pods keep running. The operator, already running since pass
one, can still actuate a spec that drops the radio, and the normal
reboot heals the machine. That is the recovery this design buys: the
control loop outlives the wedge.

## What this does not protect

A machine whose only path is its radio gets nothing from this plan.
Its boot must wait for the radio, a wedge leaves it with no running
operator and no other path, and only prevention at the driver level
keeps it alive. Plan 62 carries that prevention for the one chip the
lab has proven to need it.

## Late consequences, named

* `/etc/resolv.conf` renders after pass one from the wired
  connections. A radio that settles later re-renders it through the
  same function, adding its nameservers in interface order under the
  existing cap of three.
* k3s reads the node's address at start, so the gate demands it: a
  radio goes to the background only when the settled wired
  interfaces both route toward the endpoint and carry an address
  inside the cluster's nodeCIDR. A machine whose node address lives
  on its radio runs pass two in the foreground, because k3s must
  not start without the address it registers with. On a machine
  with a wired node address, a radio's address is additional.
* The sweep and the conditions read the same facts they read today;
  they arrive later for a background radio and carry one new state
  for a raise that did not return.

## The drill

The dev fleet has no radio, so the QEMU drills cover the pass split
and the metal covers the radio:

* A wired-only guest boots exactly as before, and the boot log shows
  pass one only.
* A guest that declares a wireless interface its hardware does not
  have: pass one settles, the boot goes on, and the status reports
  the radio's failure without the boot having waited.
* On `liken-1`, with plan 62's drill configuration: the wired path
  settles, k3s starts, and the join completes in the background with
  its verdict visible in `kubectl get machine` afterward.

## What the lab measured

The QEMU drills ran on 2026-08-26 against a dev build of this tree.
A wired-only boot printed zero wireless lines and its boot times
matched the previous release: about 0.2 seconds from hostname to
k3s, 5.2 seconds when eth0 ran a full DHCP exchange, on both
releases. A spec that declared a radio the guest does not have did
not slow the boot: the cipher pass and the background hand-off cost
32 milliseconds, k3s started 275 milliseconds after the radio's
failure, and the machine reached Ready on the wired path with the
radio reported as NoCarrier and a message naming the missing port.
The system pods settled in 17.4 seconds on that boot. The raise
deadline and the park have no QEMU drill, because the dev fleet has
no radio; the metal drill in plan 62 covers them.

The 2026-08-26 incident measured the cost of the ordering this plan
replaced: one wedged raise, zero recovery paths, one reinstall.
