# Multiple leaders: quorum

Milestone 09 — Completed. The control plane grows from one leader to
three, with quorum, through one Cluster edit.

The whole growth path is one Cluster edit that converges. spec.leaders
grows from [node-1] to three names. Every machine stages the new
document through the mechanism that milestone 8 built. There is no
separate mechanism to add a leader. sqlite, through kine, serves one
leader. More than one leader requires embedded etcd.

1. [x] Config derivation by leader count (init/k3s.go): one leader
   means exactly today's config, with sqlite and no etcd keys, because
   a single-node cluster must stay cheap to run. With more than one
   leader, the first entry in spec.leaders becomes the founding leader
   and renders cluster-init: true. On the migration boot, k3s migrates
   the sqlite datastore into embedded etcd in place. This migration is
   what makes a start on sqlite safe, rather than a dead end. Every
   other leader renders server:, which points at the founder. Init
   resolves the founder's address from the fleet's Machine manifests,
   the same way it already resolves time sources, with
   leaderAddresses. Followers stay unchanged. Rejoins keep the same
   flags on every boot: the founder keeps cluster-init, and the rest
   keep server:. This matches k3s's recommended steady state. The
   founding leader is a config-derivation role, which means the first
   name in the list. It is not etcd's raft leader, which is elected and
   can change. The code comment must state that difference clearly.
2. [x] The endpoint stays one explicit input. Followers use it for
   first contact only. After a follower joins, its k3s agent maintains
   a client-side load balancer that learns every leader's address. So a
   dead endpoint blocks only new followers from joining. It does not
   affect followers that already joined. Time queries already bypass
   the endpoint and ask each leader by address. A VIP or DNS name is a
   choice that each deployment makes, and the manifest documents the
   tradeoff.
3. [x] Quorum: three leaders, an odd number by design. There is no CEL
   rule on leader-count parity. Growth from one leader to three in one
   edit never passes through two leaders, and admission must not refuse
   a temporary even state that some future migration needs. A
   simultaneous reboot of all leaders loses quorum for a time, and
   quorum reforms from disk afterward. Milestone 13's rollout now
   reboots only one Auto-policy leader at a time, whatever its budget.
4. [x] The lab grows to five machines: three leaders (node-1 as
   founder, node-3 and node-4 new) and two followers (node-2 staying,
   node-5 new). MACs, dist directories, and manifests extend the
   existing NODE dimension. A new MEM setting on the Makefile lets
   guests use less memory, because five 4G guests do not fit on a 30G
   laptop. Followers run at 2G, and machines that will become leaders
   run at 3G. All five machines came up Ready and Converged on the
   existing cluster in less than a minute. node-3, node-4, and node-5
   boot as followers until the growth edit promotes them.
5. [x] Drills: the plan covered three things, and all three ran.

   First, the 1 to 3 growth edit, end to end: migration on node-1, two
   joiners, with followers that continue to run through it. The growth
   edit converged machine by machine. node-1's migration boot moved
   sqlite into embedded etcd in place, node-3 and node-4 joined at the
   founder's address, and the whole change needed only one kubectl
   patch plus the per-machine reboot policies.

   Second, a kill of one leader, to see the cluster keep serving while
   machine status reports the loss. With one leader dead, the cluster
   kept serving and scheduling work at quorum 2 of 3, and the revived
   leader rejoined afterward. With all three leaders dead, the API went
   dark while the followers' machines stayed up. A relaunch of the
   leaders reformed etcd from disk: the cluster re-established itself,
   and the followers reconnected without a reboot.

   Third, a try at follower-to-leader promotion on node-2, by growth of
   spec.leaders to include it. k3s's tolerance for a role flip under
   the same name was unknown, so the recorded findings were the
   deliverable, and the plan permitted a fresh rebuild of node-2 if the
   flip failed. Promotion of node-2 (a follower since milestone 6, with
   the same name and the same disks) worked: it rebooted as
   control-plane,etcd.

   Demotion caused the most problems. A reboot of node-4 as a follower
   left its Node object still claiming control-plane,etcd. It also left
   node-4 registered as an etcd member, with no real node behind the
   registration. This leftover member would have broken quorum
   arithmetic on the next leader reboot. `kubectl delete node` triggers
   k3s's etcd member-removal controller, but a kubelet whose Node
   object vanishes mid-run does not re-register. So the full demotion
   recipe became: edit the Cluster, reboot, delete the Node object,
   then boot once more. That recipe is now fully automated. The demoted
   machine's own operator detects that its Node still claims
   control-plane, writes the reboot intent, and then deletes its own
   Node object. The intent must come first, because the delete kills
   the pod that does the work. The next proven follower boot also
   removes the leftover etcd datastore, which etcd would otherwise
   refuse to let rejoin. The drill ran again with no manual steps in
   both directions: promote node-4 with one edit, and demote it with
   another.

   The drill also exposed a rejection-authority bug. The decision
   tables read the quarantine record from facts, which are frozen at
   boot. So a rejection that a revert cleared kept blocking a retry
   until the next reboot. The durable record on machineState is the
   authority now.
