---
title: Set up a portable telescope
weight: 45
description: "Keep a portable telescope's equipment off until a person carries it out, uncovers it, and polar aligns it: require the SetUp condition in spec.requires, confirm it with kubectl liken observatory set-up, let it expire at dawn, and ask the person to cover the telescope with a notify action at the end of the night. Use when a telescope is not on a fixed pier, when a reservation waits in Wait or Requires, or when nothing must power on before a person or the weather allows it."
---

# Set up a portable telescope

A portable telescope spends the day in a garage. Before a night,
someone carries it out, uncovers it, and polar aligns it, and nothing
may power on or slew it before then. When you finish this guide, a
reservation of the telescope waits until you confirm that it is set
up, your confirmation lasts until dawn by itself, and the end of the
night asks you to cover the telescope.

You need the `kubectl liken observatory` plugin. `liken plugins sync`
installs it, as
[Install the plugins](https://liken.sh/docs/reference/cli/#install-the-plugins)
describes. The examples use the example observatory from
[Install](/docs/guides/install/), in the namespace `observatory`.

## Require the person's confirmation

List the `SetUp` condition in the telescope's `spec.requires`. A
requirement with no `kind` names the resource itself:

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

A reservation of the telescope then waits in its `Requires` step,
after the observatory's server starts and before anything of the
telescope runs:

```sh
kubectl get rsv -n observatory
```

```
NAME           TELESCOPE   HOLDER    PHASE        STEP       MESSAGE                                 AGE
east-tonight   east        desktop   Activating   Requires   Waiting for Telescope east SetUp=True   2m
```

The step's summary names each condition it waits for. The telescope's server, its
pods, and the outputs of its `Switch` stay off.

Requirements on the `Observatory`, or on a device of the observatory,
hold the reservation earlier, in its `Wait` step, before the
observatory's server starts. Put a weather station's `Safe` on the
`Telescope`: the station runs on the observatory's server, so it
reports `Safe` only after that server starts.

## Let a person confirm

A person confirms by writing the `SetUp` condition into the
telescope's status. The plugin writes it:

```sh
kubectl liken observatory set-up telescope east -n observatory
```

```
Telescope east is set up until the next astronomical dawn at its Observatory
```

The reservation leaves `Requires` at once, and activation runs. The
condition's message names the person, from the user that the API
server reports for your credentials, such as
`Set up by ada`. `--by` names someone else.

The command takes these flags:

| Flag | What it does |
| --- | --- |
| `--until` | When the confirmation expires, as an RFC 3339 time such as `2026-10-13T04:30:00Z`, or as a duration from now, such as `6h`. With none, it expires at dawn. |
| `--by` | The person the message names, in place of the user the API server reports. |
| `-n`, `--namespace` | The namespace of the resource. |
| `--force` | Silences the warning that the CLI's version differs from the operator's. The command runs either way. |
| `--version` | Prints the CLI's version and exits. |

`set-up` and `take-down` accept the `Observatory`, the `Telescope`,
and each device kind, by name, singular, or plural, such as `mount`,
`Mount`, or `mounts`.

The condition is in status, not in spec, because it records what a
person saw, and a GitOps tool that applies the telescope's spec from
a repository never removes it. The plugin writes it with a server-side
apply to the status subresource, so it changes only `SetUp` and
`status.setUpUntil`, and the operator keeps both as you wrote them.

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

The plugin also reads the operator's `Deployment` to compare
versions. A person who cannot read it gets a line on stderr, and the
command still runs.

## When the confirmation expires

A confirmation lasts until the first astronomical dawn after you
wrote it, at the `Observatory`'s `spec.location`: the moment the sun
rises through 18 degrees below the horizon. A second `set-up` in the
same night keeps the first one's time, and so its dawn. With
`--until`, it lasts until that time instead. At the expiry, the
operator sets the condition `False` with the reason `Expired`, such as
`Expired at 2026-10-13T08:44:52Z: set up by ada`, and posts one
`Event`. Nobody has to withdraw it in the morning.

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
`<none>` while the telescope is not set up, and after the
confirmation expires. `status.setUpUntil` holds only what you wrote,
so it is empty when the confirmation lasts until dawn. To read the
condition itself:

```sh
kubectl get telescope east -n observatory \
  -o jsonpath='{.status.conditions[?(@.type=="SetUp")]}'
```

A session that already runs keeps running when the confirmation
expires: a requirement holds only the start of a session. A reservation
that still waits at dawn ends, with the telescope's equipment never
powered on, and its `Requires` step's summary says why, such as
`Dawn came at 2026-10-13T08:44:52Z while waiting for Telescope east SetUp=True, so the session did not start`.

## Cover the telescope at the end of the night

The `notify` action in the telescope's `deactivation` asks a person to
cover it. It never waits: the mount parks and the cameras warm on
their own. The telescope's `AttentionNeeded` condition turns `True`
with the request as its message, and a `Warning` Event with the
reason `Notified` says the same:

```sh
kubectl describe telescope east -n observatory
```

Once you cover the telescope and carry it in, take it down:

```sh
kubectl liken observatory take-down telescope east -n observatory
```

`SetUp` turns `False` with the reason `TakenDown`, and
`AttentionNeeded` turns `False` with the reason `Acknowledged` and the
message of your take-down, such as `Taken down by ada`. The
next activation of the telescope clears `AttentionNeeded` too, with
the reason `Activated`.
