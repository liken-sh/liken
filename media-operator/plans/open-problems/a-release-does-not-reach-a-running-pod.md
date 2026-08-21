# A release does not reach a running pod

The operator stamps the player image into a pod once, when it creates
the pod, and never again. So a new release reaches a `Play` that starts
after the release, and it does not reach a `Play` that was already
running when the release landed. The standing remote pod is the same: the
operator builds it once per `Remote` and leaves it, so it keeps whatever
image it started with until something deletes it.

This showed on the first hardware drill. The operator rolled to a new
release, but the running playback pod kept the previous release's player
image, and the standing remote pod was a release older still, because
neither had a reason to be recreated. Every pod was healthy and correct
for the code it ran; it was just not the code that was released.

The cause is the pod's own contract. A pod's container set is immutable,
so a new image is a new pod, and the operator recreates a playback pod
only when a `Player` reshapes it. A release is not a `Player` edit, so it
triggers no recreate. This is deliberate for playback: a film should not
restart because an unrelated release shipped, and plan 04's graceful
recreate exists precisely to keep a `Player` edit from interrupting the
film, not to roll every pod on every release. The standing remote pod has
no graceful recreate at all, so rolling it drops the controller's input
for the moment the pod restarts.

The answer is a policy question, not a mechanism gap. The mechanism to
roll a pod already exists: delete it, and the operator rebuilds it on the
current image. The question is when the operator should spend a restart.
Three shapes are on the table. The operator could compare each running
pod's image to the release it now stamps and recreate a pod that drifted,
which makes a release reach every pod at the cost of a restart per pod. It
could roll only the standing remote pods, whose restart is a blink of lost
input rather than an interrupted film, and leave a playback pod to pick up
the new image when its `Play` next starts. Or it could leave the policy to
the operator's owner, who deletes a pod when a release carries a fix worth
the restart, which is what happens today by hand.

Until this is decided, a release reaches the running pods only when a
person recreates them. Deleting a `Play` and reapplying it rolls the
playback pod; deleting the standing remote pod rolls the reader. Both are
safe, and both are manual.
