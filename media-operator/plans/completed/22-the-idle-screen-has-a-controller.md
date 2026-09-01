# 22, The idle screen has a controller

A `Player`'s idle screen is one pod this operator builds: the idle
client, the idle command sidecar, and a standing claim on the unit's
screen. `spec.idle.image` swaps the client. It cannot swap the pod. A
client that needs a helper process, a mount, or a volume of its own
cannot run as the idle screen, because the pod has exactly the
containers this operator gives it.

This plan makes the idle screen's controller a named choice on the
`Player`, the way a `GatewayClass` names its `controllerName` and a
`StorageClass` names its provisioner. This operator draws the idle
screen only when the name is its own. Any other name hands the screen
to whichever operator answers to it, and this operator publishes on the
`Player` status the two facts that operator needs: the claim to
reference and the requests it carries. This operator keeps the claim,
the timers, and the panel policy in every case, and it never learns
what the other operator is.

## The field

`controller` joins `IdlePolicy`, beside `image`, on the `Player` and on
`MediaPreferences`. It resolves through the same tiers `image` does: the
`Player`'s block, then the household default, then the built-in.

A value is a domain-qualified name, `<domain>/<name>`, and the CRD
holds it to that pattern. Two names are this operator's own:

* `media.liken.sh/idle-screen`, the built-in default. This operator
  stands the claim, the idle client pod, and the idle command pod.
* `media.liken.sh/none`. Nothing draws an idle screen on this unit.
  This operator stands no claim and no pod, and it leaves the panel
  alone. The precedent is `kubernetes.io/no-provisioner`.

Every other name is a delegate. This operator stands the claim and the
idle command pod, stands no client pod, and compares the name to its
two constants and nothing else. `image` has no effect under a delegate,
because the delegate brings its own pod.

The `kubectl get players` table gains an `Idle` column that shows the
resolved controller, so a person reads who draws each screen.

## The status

`status.idle` is what a delegate reads. A delegate acts on the status
and never on the spec, because the spec may inherit its controller from
`MediaPreferences` and only this operator resolves the tiers.

    status:
      idle:
        controller: library.liken.sh/media-browser
        claim: studio-lg-idle-devices
        requests: [draw, render]

`controller` is the resolved name. `claim` is the standing claim in the
`Player`'s namespace, which the delegate's pod references by name in its
`resourceClaims`. `requests` are the request names the claim carries,
one per `resources.claims` entry the delegate's container states. Under
`media.liken.sh/none` the block holds the controller alone.

## The claim stays here

The delegate's pod references the claim this operator built. This
operator keeps the claim because the claim is where the hardware
knowledge is: the display selector, the render request, and the
immutable-claim rule that a mode change replaces the claim. A delegate
that held its own claim would have to learn all of that.

The DRA facts this rests on, read on `liken-1` under k3s 1.36.3:

* A `ResourceClaim` is a namespaced object, and any pod in the
  namespace references it by name.
* An allocated claim's `status.reservedFor` names each pod that holds
  it.
* An allocated claim carries the `resource.kubernetes.io/delete-protection`
  finalizer, so a delete stays in Terminating until every holder is
  gone.

So when the claim this pass would build differs from the live one, the
standing reconcile deletes every pod `reservedFor` names, then the
claim. It already deletes its own pod first for the same reason; now
the holder list decides, and the delegate's pod goes on the same rule.
The next pass finds the claim absent and creates it. The delegate finds
its pod absent and creates it, the way any level-triggered operator
does. Until the claim returns, a replacement pod parks `Pending` on a
claim that does not exist yet, and the drill proves it schedules when
the claim comes back.

The same rule handles the switch to `media.liken.sh/none`: the claim is
no longer wanted, its holders go, then it goes, and a delegate that has
not read the change yet has no claim to schedule against.

The delete verb on pods already exists. This plan adds no RBAC verb,
and `deploy/rbac.yaml` states that the verb now also deletes pods this
operator did not create.

## The idle command pod stands on its own

The idle command sidecar holds the fade and off windows, the panel
desire, and the press gate. It reads the bus and writes the bus, and it
holds no device. Today it is a native sidecar in the idle client pod.
Under a delegate there is no client pod of this operator's to put it in,
and the delegate must not learn its image or its environment.

So it becomes a standing pod of its own, `<player>-idle-command`, with
no claim, for every unit whose controller is anything but
`media.liken.sh/none`. The idle client pod keeps the client alone. One
shape serves every unit, so the delegated path is the same path the
default exercises every day. The idle command pod follows the template
hash the way the other standing pods do.

The cost is one more pod per unit. The image is this operator's binary
on `scratch`, and the drill records its resident memory on the box.

## Considered and set aside

* **Extra containers and volumes declared on `spec.idle`.** The
  earlier shape, from the library layer's plan 06. It makes this
  operator carry another layer's pod definition without naming it, and
  every new need becomes another passthrough field. A controller name
  moves the whole pod to the layer that defines it.
* **The delegate holds its own claim.** It would have to reproduce the
  display selector, the render request, and the replace-on-change rule
  that this operator already runs. It would also have to learn the
  cluster's display-draw class, which is this operator's environment.
* **This operator lends the screen by proxying the socket.** More
  machinery than a claim name on a status, and DRA already shares the
  draw device across pods.
* **The timers move into the operator process.** It would remove a
  pod, but plan 12 put the timers beside the screen on purpose: the
  operator restarts without dropping a unit's fade, and the operator
  holds no per-unit clocks.
* **A sidecar-only pod under delegation, and the combined pod
  otherwise.** Two pod shapes, and the delegated shape would run only
  where a household delegates. One shape runs everywhere.
* **Bus topics on `status.idle`.** A delegate client that reads the
  bus needs the topic base, which is this operator's configuration.
  The first delegate reads no bus, so the fields wait for a consumer.

## How the work is proved

On `liken-1`, with the library layer's browser as the delegate:

1. Set `lab-portable`'s controller to `library.liken.sh/media-browser`.
   The idle client pod goes, the idle command pod stands, and
   `status.idle` names the claim and its requests. The delegate's pod
   comes up holding the claim, read from `reservedFor`, and draws on
   the screen.
2. Remove `spec.render` from the `Player`. The claim's template
   changes, the delegate's pod is deleted first, the claim follows, and
   the replacement schedules once the claim returns. Restore `render`
   and watch the same cycle.
3. Set `media.liken.sh/none`. The claim and both pods go, and the
   screen shows the compositor's background.
4. Clear the field. The default returns and the stock idle screen draws
   again.
5. Record the idle command pod's resident memory.
