# Who owns the pairing UX

Open problem. The operator runs bluetoothd, so it is the only layer
that can offer pairing. Whether it should offer pairing is not
decided.

## Why it is a question and not a feature

Pairing is a privileged act on a radio that reaches past the house
walls. A pairing window accepts a bond from whoever is in range and
pressing the button, and the operator has no way to tell the person
who meant it from anybody else. An API that opens that window is an
API that a mistake, or a token that leaked, can open.

The counter-argument is that the by-hand path has the same reach and
worse controls. Today a person pairs by running `bluetoothctl` inside
the operator's pod, which the README documents under "Pairing". That
path needs `kubectl exec` into a pod in `liken-system`, which is a
broader grant than pairing, and it leaves no record of who paired
what.

## The leaning

A pairing-request CRD, in a later iteration. A person creates a
resource that names the adapter and a duration. The operator opens a
pairing window for that long and closes it when the duration runs out.
The resource's status reports which controller paired, or why none
did.

That shape gives three things the `bluetoothctl` path does not. The
act is deliberate, because somebody created an object for it. The act
is audited, because the object and its status stay in the API. The act
is time-bounded, because the duration is on the object and the
operator closes the window without anybody coming back.

It also gives `BLUETOOTH_CLASSIC_BONDED_ONLY` a home. A DualSense's
first pairing needs that setting off, and today turning it off is a
`kubectl set env` on the StatefulSet, which restarts the whole
workload twice for one pairing. A pairing request could relax the
setting for the window it opens and put it back when the window
closes.

## What is not decided

- Whether the operator offers pairing at all, or whether pairing stays
  a by-hand act on purpose, because a manual step is a control.
- What the CRD's scope is. A namespaced resource in `liken-system`
  means pairing needs write access to that namespace, which is close
  to the `kubectl exec` grant it replaces.
- What the status says when two controllers pair inside one window.

This is recorded in liken's
[milestone 58](https://github.com/liken-sh/liken/blob/main/plans/completed/58-the-bluetooth-operator.md)
as well. It belongs here, with the operator that would serve it.
