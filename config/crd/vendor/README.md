# Vendored CRDs

`monitoring.coreos.com_{prometheusrules,servicemonitors,podmonitors}.yaml` are
copied unmodified from the
[prometheus-operator](https://github.com/prometheus-operator/prometheus-operator)
release matching the `github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring`
version pinned in `go.mod` (currently v0.94.0):

```bash
curl -sSL -o config/crd/vendor/monitoring.coreos.com_prometheusrules.yaml \
  https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/v0.94.0/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
```

They exist so `internal/controller`'s envtest suite has real CRD schemas to
validate the `PrometheusRule`/`ServiceMonitor`/`PodMonitor` objects the
reconciler applies against -- envtest only bundles core Kubernetes API
types, not third-party operator CRDs. They are **not** installed by this
operator's Helm chart; a real cluster is expected to already run Prometheus
Operator (or kube-prometheus-stack), which owns these CRDs.

Re-run the curl commands above (for all three files) whenever the
`monitoring` module version in `go.mod` changes.
