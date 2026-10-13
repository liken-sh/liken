---
name: set-up-a-portable-telescope
description: "Keep a portable telescope's equipment off until a person carries it out, uncovers it, and polar aligns it: require the SetUp condition in spec.requires, reserve the telescope and watch the reservation wait in its Requires step, confirm with kubectl liken observatory set-up, read the SET UP UNTIL column, let the confirmation expire at dawn or at --until, answer the notify action that asks the person to cover the telescope at the end of the night, and run take-down. Use when a telescope is not on a fixed pier, when a reservation waits in Wait or Requires with Waiting for ... SetUp=True, when AttentionNeeded is True with the reason Notified, when SetUp is False with the reason Expired, or when nothing must power on before a person or the weather allows it."
---

This skill is the guide at https://liken.sh/observatory/docs/guides/set-up-a-portable-telescope/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Set up a portable telescope

A portable telescope spends the day in a garage. Before a night,
someone carries it out, uncovers it, and polar aligns it, and nothing
may power on or slew it before then. When you finish this guide, a
reservation of the telescope waits until you confirm that it is set
up, your confirmation expires at dawn by itself, and the end of the
night asks you to cover the telescope.

You need the `kubectl liken observatory` plugin. Install it with
`liken plugins sync <deployment-dir>`, as
[Install the plugins](https://liken.sh/docs/reference/cli/#install-the-plugins)
describes. The plugin and the operator come from the same release, so
run the command again after you upgrade the operator. The examples
use the example observatory from [Install](https://liken.sh/observatory/docs/guides/install/), in
the namespace `observatory`.

## 1. Require the person's confirmation

List the `SetUp` condition in the telescope's `spec.requires`, and add
a `notify` action to its `deactivation`. A requirement with no `kind`
names the resource itself:

```yaml
apiVersion: observatory.liken.sh/v1alpha1
kind: Telescope
metadata:
  name: east
spec:
  observatory: lab
  requires:
    - type: SetUp
    - {kind: WeatherStation, name: lab, type: Safe}
  deactivation:
    - notify: Cover the telescope and carry it in
```

The second requirement keeps the telescope off while the weather
station reports bad weather. Each entry is `{kind, name, type,
status}`, the same form as an action's `requires`, and `status` is
`"True"` unless you set it. Any device can list requirements too,
such as a camera whose dew strap someone fits by hand.

Put the requirements on the `Telescope` or on its devices. The
operator checks those in the reservation's `Requires` step, after the
observatory's INDI server starts and before anything of the telescope
runs. Requirements on the `Observatory`, or on a device of the
observatory, hold the reservation earlier, in its `Wait` step, before
the observatory's server starts. So a weather station's `Safe` belongs
on the `Telescope`: the station runs on the observatory's server, and
it reports `Safe` only after that server starts.

## 2. Reserve the telescope

Create a reservation as [Reserve a telescope](https://liken.sh/observatory/docs/guides/reserve-a-telescope/)
describes. It waits in its `Requires` step, and the message names
each condition that does not hold yet:

```sh
kubectl get rsv -n observatory
```

```
NAME           TELESCOPE   HOLDER    PHASE        STEP       MESSAGE                                 AGE
east-tonight   east        desktop   Activating   Requires   Waiting for Telescope east SetUp=True   2m
```

While it waits, the telescope's server, its pods, and the outputs of
its `Switch` stay off. You can create the reservation in the
afternoon, and it waits for you.

## 3. Confirm that the telescope is set up

Once the telescope is out, uncovered, and polar aligned, confirm it:

```sh
kubectl liken observatory set-up telescope east -n observatory
```

```
Telescope east is set up until the next astronomical dawn at its Observatory
```

The plugin writes the `SetUp` condition into the telescope's status.
The reservation leaves `Requires` at once, activation runs, and the
reservation turns `Ready`. The condition's message names you, from the
user that the API server reports for your credentials:

```sh
kubectl get telescope east -n observatory \
  -o jsonpath='{.status.conditions[?(@.type=="SetUp")]}'
```

```json
{"lastTransitionTime":"2026-10-12T23:10:04Z","message":"Set up by ada","reason":"SetUp","status":"True","type":"SetUp"}
```

The `SET UP UNTIL` column of `kubectl get` shows when the confirmation
expires:

```sh
kubectl get telescopes -n observatory
```

```
NAME   OBSERVATORY   PHASE   RESERVATION    HOLDER    HOST                             PORT   SET UP UNTIL           AGE
east   lab           Ready   east-tonight   desktop   east-telescope.observatory.svc   7624   2026-10-13T08:44:52Z   12d
```

The operator writes that time into `status.setUpExpiryTime`: your
`--until` when you gave one, and otherwise the dawn. The column is
`<none>` while the resource is not set up. `status.setUpUntil` holds
only what you wrote with `--until`, so it is empty when the
confirmation lasts until dawn.

The command takes these flags:

| Flag | What it does |
| --- | --- |
| `--until` | When the confirmation expires, as an RFC 3339 time such as `2026-10-13T04:30:00Z`, or as a duration from now, such as `6h`. With none, it expires at dawn. `take-down` refuses it. |
| `--by` | The person the message names, in place of the user the API server reports. |
| `-n`, `--namespace` | The namespace of the resource. |
| `--context`, `--kubeconfig` | The kubeconfig context and file, as for `kubectl`. |
| `--force` | Silences the warning that the plugin's version differs from the operator's. The command runs either way. |
| `--version` | Prints the plugin's version and exits. |

`set-up` and `take-down` accept the `Observatory`, the `Telescope`,
and each device kind, by name, singular, or plural, such as `mount`,
`Mount`, or `mounts`.

The condition is in status, not in spec, because it records what a
person saw. A GitOps tool that applies the telescope's spec from a
repository never removes it. The plugin writes it with a server-side
apply to the status subresource, so it changes only `SetUp` and
`status.setUpUntil`, and the operator keeps both as you wrote them
until the confirmation expires.

## 4. Cover the telescope at the end of the night

The reservation ends at its `end` time, or when you delete it. Its
`Deactivation` step runs the telescope's `notify` action, which asks
you to cover the telescope. The action never waits for you: the mount
parks and the cameras warm on their own. The telescope's
`AttentionNeeded` condition turns `True` with the reason `Notified`
and the request as its message, and the transition posts a `Warning`
Event:

```sh
kubectl events -n observatory --for telescope/east
```

```
LAST SEEN   TYPE      REASON     OBJECT           MESSAGE
1m          Warning   Notified   Telescope/east   Cover the telescope and carry it in
```

An alert on `AttentionNeeded`, or a script that runs `kubectl wait
--for=condition=AttentionNeeded telescope/east -n observatory`, can
send the request to your phone.

Wait until the reservation's `SafeToPowerOff` is `True`, then cover the
telescope, carry it in, and take it down:

```sh
kubectl liken observatory take-down telescope east -n observatory
```

```
Telescope east is taken down
```

`SetUp` turns `False` with the reason `TakenDown` and the message
`Taken down by ada`. `AttentionNeeded` turns `False` with the reason
`Acknowledged` and the same message, so the condition records who
answered the request. If you never take the telescope down, the
telescope's next activation clears `AttentionNeeded` with the reason
`Activated`.

A take-down during a session stops nothing. A requirement holds only
the start of a session, so the equipment runs until the reservation
ends.

## When the confirmation expires

A confirmation lasts until the first astronomical dawn after you
wrote it, at the `Observatory`'s `spec.location`: the moment the sun
rises through 18 degrees below the horizon. A second `set-up` in the
same night keeps the first one's time, and so its dawn. With
`--until`, it lasts until that time instead. At the expiry, the
operator sets the condition `False` with the reason `Expired` and a
message such as `Expired at 2026-10-13T08:44:52Z: set up by ada`, and
posts one `Event`. Nobody has to withdraw it in the morning, and the
next night's reservation waits for a new `set-up`. An expiry does not
answer a `notify` request, so `AttentionNeeded` stays `True` until
you take the telescope down or its next activation begins.

A session that already runs keeps running when the confirmation
expires. A reservation that still waits at dawn ends, with the
telescope's equipment never powered on. Its `Requires` step is
`Skipped`, and the summary says why, such as
`Dawn came at 2026-10-13T08:44:52Z while waiting for Telescope east SetUp=True, so the session did not start`.
The reservation is `Released`, so create a new one for the next
night.

## Let other people confirm

A person who confirms needs `get` on the resource and `patch` on its
status. This `Role` grants that for the telescopes and mounts of one
namespace, and `list` for the shell's completion of their names:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: observatory-set-up
  namespace: observatory
rules:
  - apiGroups: [observatory.liken.sh]
    resources: [telescopes, mounts]
    verbs: [get, list]
  - apiGroups: [observatory.liken.sh]
    resources: [telescopes/status, mounts/status]
    verbs: [patch]
```

The plugin also lists the `Deployment`s of every namespace, to find
the operator's and compare versions. A person who cannot list them
gets a line on stderr, and the command still runs.
