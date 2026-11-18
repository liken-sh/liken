# kube-prometheus

A `MetricsSource` for kube-prometheus's Prometheus and Alertmanager. It names the cAdvisor, kubelet, and kube-state-metrics series to query, and the labels on the alert rules.
With it, the machines band and the pod bars show usage, the page of a volume shows how full it is, "Now" lists each firing alert on the object it is about, and a machine loss page shows the alerting path.
