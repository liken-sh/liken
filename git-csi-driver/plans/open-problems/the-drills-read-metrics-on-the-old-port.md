# The drills read metrics on the old port

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it. On 2026-11-19, `grep -n 9808 lab/drills/*.sh` found the
old port in drills 04, 05, 06, 10, and 12, while `cli.go` and
`deploy/node.yaml` use `:9200`. No drill ran, so the failed waits are
not seen, and no unit test can show them: a run of drill 04 in the lab
does.

Drills 04, 05, 06, 10, and 12 in `lab/drills/` read the node plugin's
metrics through the API server's pod proxy at
`pods/$pod:9808/proxy/metrics`. The node plugin serves its metrics on
port 9200: `--metrics` defaults to `:9200` in `cli.go`, and
`deploy/node.yaml` passes `--metrics=:9200` and declares
`containerPort: 9200`. Commit `aa1210a7` moved the metrics from
9808 to 9280, and commit `b2ab5a85` moved them from 9280 to 9200. Each
changed `cli.go`, its tests, and the manifests, and neither changed the
drills.

Each read ends in `|| true`, so a failed proxy request prints nothing
and does not stop the drill. `wait_for_metric` in drills 04, 05, and
06 then waits until its deadline and fails. `demanded_pulls` in
drills 10 and 12 finds no `git_csi_demanded_pulls_total` line, so the
step that counts pulls sees no pull.

## What would confirm it

A run of drill 04 in the lab: the step that waits for a metric fails
at its deadline, and
`kubectl get --raw /api/v1/namespaces/<namespace>/pods/<pod>:9200/proxy/metrics`
on the same pod returns the metrics.

## What a fix has to decide

Whether the five copies of the proxy read become one helper that the
drills share, so a later change of the port changes one line. A drill
could also read the port from the pod's `containerPort` in place of a
number in the script.
