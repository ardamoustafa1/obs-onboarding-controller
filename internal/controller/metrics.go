package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Dogfooding (requirement 6): the operator's own golden signals --
// reconcile latency, reconcile error rate, and (via the client-go
// workqueue metrics controller-runtime already registers) queue depth --
// are exposed on the same /metrics endpoint every other workload's
// ServiceMonitor scrapes, and are onboarded through this system exactly
// like any other service: see config/samples/obs_v1_serviceobservability_self.yaml
// and the meta-monitoring note in README.md.
var (
	reconcileDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "obs_onboarding_reconcile_duration_seconds",
		Help:    "Time spent in one ServiceObservability reconcile, by outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"outcome"})

	reconcileErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "obs_onboarding_reconcile_errors_total",
		Help: "ServiceObservability reconcile errors, by which aspect failed.",
	}, []string{"aspect"})

	syncedServices = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "obs_onboarding_services_ready",
		Help: "1 if a ServiceObservability's Ready condition is currently True, else 0.",
	}, []string{"namespace", "name", "environment"})
)

func init() {
	metrics.Registry.MustRegister(reconcileDuration, reconcileErrorsTotal, syncedServices)
}
