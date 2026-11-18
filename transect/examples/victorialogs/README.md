# VictoriaLogs

A `LogSource` for VictoriaLogs and its LogsQL query language. It expects log lines that an OpenTelemetry collector ships with their Kubernetes attributes and level names.
With it, the last lines of a pod come from the log store with their levels, log patterns are searched across the cluster, and each namespace counts its error lines.
