# An empty METRICS_ADDR does not turn metrics off

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

The comment in `main.go` says that an empty `METRICS_ADDR` disables
the metrics listener, "so a run on a workstation and every test in
this repository listen on no port". `api.go` has the same intent: it
opens the listener only when `address != ""`. Both read the variable
with `envOr("METRICS_ADDR", defaultMetricsAddr)`, and `envOr` returns
the fallback `:9200` when the value is empty. So an empty
`METRICS_ADDR` listens on `:9200`, and no setting turns the listener
off in the operator or in display-api.

## Evidence

- `main.go`, `envOr`: `if value := os.Getenv(name); value != ""`,
  otherwise `return fallback`.
- `metrics.go`: `defaultMetricsAddr = ":9200"`.
- `main.go` line 184 has the comment, and `readings.listen` gets the
  result of `envOr`.
- `api.go`: the `address != ""` test is always true.

## The reproduction

The test set `METRICS_ADDR` to the empty string with `t.Setenv`, read
the address with `envOr("METRICS_ADDR", defaultMetricsAddr)` as `main`
and `api.go` do, and asserted the empty string. It failed with:

```
the metrics address for METRICS_ADDR= is ":9200", want "" so that listen opens no port
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

Should setting `METRICS_ADDR` to the empty string turn the metrics
port off, or should metrics always be on?

The lookup can use `os.LookupEnv`, so a variable that is set and
empty differs from one that is unset. Or the comment and the
`address != ""` test go, and the listener is always on.
`deploy/operator.yaml` and `deploy/api.yaml` set `METRICS_ADDR` to
`:9200`, so neither choice changes a deployed operator.
