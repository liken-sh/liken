# Multiple leaders: quorum

Milestone 9 — Done

Multiple leaders: quorum for the control plane. The whole
growth path is one Cluster edit converging: spec.leaders
grows from [node-1] to three names, every machine stages the
new document via milestone 8, and no separate "add a leader"
mechanism exists. sqlite (via kine) serves one leader; more
than one means embedded etcd.
1. [x] Config derivation by leader count (init/k3s.go): one
   leader means exactly today's config, sqlite and no etcd
   keys, because single-node should stay cheap. More than one:
   the first entry in spec.leaders is the founding leader and
   renders cluster-init: true. On the migration boot, k3s
   migrates the sqlite datastore into embedded etcd in place,
   which is what made starting on sqlite safe rather than a
   dead end. Every other leader renders server: pointing at
   the founder, resolved from the fleet's Machine manifests
   the way time sources already are (reuse leaderAddresses).
   Followers are unchanged. Rejoins keep the same flags every
   boot (the founder keeps cluster-init, the rest keep
   server:), which is k3s's recommended steady state. The
   founding leader is a config-derivation role, meaning the
   first name in the list. It is not etcd's raft leader, which
   is elected and moves; the comment must draw that
   distinction.
2. [x] The endpoint stays one explicit input. Followers use it for
   first contact only: after joining, k3s agents maintain a
   client-side load balancer that learns every leader, so a
   dead endpoint strands only new followers, never running
   ones (and time queries already bypass it, asking each by
   address). A VIP or DNS name is a deployment's choice to
   make; the manifest documents the tradeoff.
3. [x] Quorum: three leaders, an odd number by design. No CEL rule
   on leader-count parity: growing 1→3 in one edit never
   passes through two, and a transient even state during some
   future migration shouldn't be refused at admission. A
   simultaneous all-leader reboot loses quorum transiently
   and reforms from disk; milestone 13's rollout now keeps
   Auto-policy leaders to one at a time regardless of budget.
4. [x] The lab grows to five machines: three leaders (node-1
   founding, node-3 and node-4 fresh) and two followers (node-2
   staying, node-5 new). MACs, dist dirs, and manifests extend
   the existing NODE dimension, plus a MEM knob on the
   Makefile, since five 4G guests don't fit on a 30G laptop
   (followers run at 2G, leaders-to-be at 3G). All five came
   up Ready and Converged on the existing cluster in under a
   minute, node-3/4/5 booting as followers until the growth
   edit promotes them.
5. [x] Drills: the 1→3 growth edit end to end (migration on
   node-1, two joiners, followers riding through it); kill one
   leader and watch the cluster keep serving while machine
   status reports the loss; then ATTEMPT follower→leader
   promotion on node-2 by growing spec.leaders to include it.
   k3s's tolerance for a same-name role flip was uncertain, so
   recorded findings were the deliverable, and node-2 could be
   rebuilt fresh if the flip failed. All of it ran, and the
   findings were better than feared. The growth edit converged
   machine by machine: node-1's migration boot moved sqlite
   into embedded etcd in place, node-3 and node-4 joined at
   the founder's address, and the whole thing was one kubectl
   patch plus per-machine reboot policies. Promotion of node-2
   (a follower since milestone 6, same name, same disks)
   worked cleanly: it rebooted straight into
   control-plane,etcd. Demotion turned out to be the rough
   edge: rebooting node-4 as a follower left its Node object
   claiming control-plane,etcd and, worse, left its etcd
   membership registered, a phantom member that would have
   broken quorum arithmetic on the next leader reboot.
   `kubectl delete node` triggers k3s's etcd member-removal
   controller, but a kubelet whose Node vanishes mid-run won't
   re-register, so the full demotion recipe was: Cluster edit,
   reboot, delete the Node object, one more boot. That recipe
   is now fully automated. The demoted machine's own operator
   notices its Node still claims control-plane, writes the
   reboot intent, and then deletes its own Node; the intent
   comes first, because the delete kills the very pod doing
   it. The next proven follower boot also purges the leftover
   etcd datastore, which etcd would otherwise refuse to let
   rejoin. The drill re-ran with no manual steps in both
   directions: promote node-4 with one edit, demote it with
   another. It also exposed a rejection-authority bug: the
   decision tables read the quarantine record from facts,
   which are frozen at boot, so a rejection cleared by a
   revert kept blocking a retry until reboot. The durable
   record on machineState is the authority now. The loss
   drills: with one leader dead the cluster kept serving and
   scheduling (quorum 2 of 3) and the revived leader rejoined.
   With ALL THREE leaders dead the API went dark while the
   followers' machines stayed up, and relaunching the leaders
   reformed etcd from disk; the cluster re-established itself
   and the followers reconnected without rebooting.
