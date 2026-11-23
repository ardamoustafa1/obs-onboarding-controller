/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types set on ServiceObservability.status.conditions. Each is
// independently True/False/Unknown so a partial failure (e.g. Grafana is
// down but the PrometheusRule still applied) is visible per-aspect instead
// of collapsing everything into a single boolean.
const (
	ConditionTypeReady                = "Ready"
	ConditionTypeDashboardSynced      = "DashboardSynced"
	ConditionTypeAlertsSynced         = "AlertsSynced"
	ConditionTypeServiceMonitorSynced = "ServiceMonitorSynced"
	ConditionTypeCatalogSynced        = "CatalogSynced"
)

// ServiceType mirrors obs-manifest-schema's spec.serviceType enum.
// +kubebuilder:validation:Enum=http;grpc;worker;batch;database
type ServiceType string

const (
	ServiceTypeHTTP     ServiceType = "http"
	ServiceTypeGRPC     ServiceType = "grpc"
	ServiceTypeWorker   ServiceType = "worker"
	ServiceTypeBatch    ServiceType = "batch"
	ServiceTypeDatabase ServiceType = "database"
)

// SLOSpec mirrors obs-manifest-schema's $defs/slo.
type SLOSpec struct {
	// availabilityTarget is the fraction of requests expected to succeed
	// over the 30 day SLO window, e.g. 0.999 for "three nines".
	// +kubebuilder:validation:Minimum=0.9
	// +kubebuilder:validation:Maximum=0.999999
	AvailabilityTarget float64 `json:"availabilityTarget"`

	// latencyTargetMs is the maximum acceptable latency, in milliseconds,
	// at latencyPercentile.
	// +kubebuilder:validation:Minimum=1
	LatencyTargetMS int32 `json:"latencyTargetMs"`

	// latencyPercentile is the percentile the latency target applies to.
	// +kubebuilder:validation:Enum=50;90;95;99
	LatencyPercentile int32 `json:"latencyPercentile"`
}

// GoldenSignalsSpec mirrors obs-manifest-schema's $defs/goldenSignals.
type GoldenSignalsSpec struct {
	// trafficMetric is the Prometheus counter used as the request-volume
	// denominator for error ratio and burn-rate calculations.
	TrafficMetric string `json:"trafficMetric"`

	// errorMetric is the Prometheus counter of failed requests/jobs.
	ErrorMetric string `json:"errorMetric"`

	// latencyMetric is the base name (without _bucket) of a Prometheus
	// histogram. Required for http, grpc, and database service types.
	// +optional
	LatencyMetric string `json:"latencyMetric,omitempty"`

	// processingDurationMetric is the worker/batch equivalent of
	// latencyMetric. Required for worker and batch service types.
	// +optional
	ProcessingDurationMetric string `json:"processingDurationMetric,omitempty"`

	// saturationMetrics are 0..1 utilization-ratio gauges (CPU, memory,
	// queue depth, ...) used for saturation early-warning alerts.
	// +kubebuilder:validation:MinItems=1
	SaturationMetrics []string `json:"saturationMetrics"`
}

// DashboardsSpec mirrors obs-manifest-schema's $defs/dashboards.
type DashboardsSpec struct {
	// template selects the obs-golden-signal-templates entry point, e.g.
	// "http-service-v2". Must match spec.serviceType.
	Template string `json:"template"`
}

// ExtraAlertRule is a hand-authored alert rule merged alongside the
// generated burn-rate/latency/saturation rules. Mirrors
// obs-alert-rule-templates' internal/manifest.ExtraRule.
type ExtraAlertRule struct {
	Name string `json:"name"`
	Expr string `json:"expr"`
	// +optional
	For string `json:"for,omitempty"`
	// +kubebuilder:validation:Enum=page;warning
	Severity string `json:"severity"`
	// +optional
	Summary string `json:"summary,omitempty"`
}

// AlertingSpec mirrors obs-manifest-schema's $defs/alerting.
type AlertingSpec struct {
	// burnRateAlerts enables the generated multi-window multi-burn-rate,
	// latency-SLO and saturation PrometheusRule groups.
	// +kubebuilder:default=true
	BurnRateAlerts bool `json:"burnRateAlerts"`

	// extraRules are merged into their own "<service>.extra" rule group.
	// +optional
	ExtraRules []ExtraAlertRule `json:"extraRules,omitempty"`
}

// OwnershipSpec mirrors obs-manifest-schema's v2 $defs/ownership.
type OwnershipSpec struct {
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9._/-]*$`
	Team string `json:"team"`
	// +kubebuilder:validation:Pattern=`^#[a-z0-9_-]+$`
	SlackChannel     string `json:"slackChannel"`
	PagerDutyService string `json:"pagerDutyService"`
}

// ScrapeConfigSpec optionally drives ServiceMonitor/PodMonitor generation.
// If omitted, no monitor object is created and the team is assumed to
// manage scraping themselves (e.g. via annotations).
type ScrapeConfigSpec struct {
	// selector picks the Service (or Pod, if podMonitor is true) to scrape.
	Selector metav1.LabelSelector `json:"selector"`

	// port is the named port to scrape.
	Port string `json:"port"`

	// path is the metrics HTTP path.
	// +kubebuilder:default="/metrics"
	Path string `json:"path,omitempty"`

	// interval is the scrape interval, e.g. "30s".
	// +kubebuilder:default="30s"
	Interval string `json:"interval,omitempty"`

	// podMonitor selects a PodMonitor instead of a ServiceMonitor, for
	// services without a stable Service object (e.g. some batch/worker
	// deployments).
	// +optional
	PodMonitor bool `json:"podMonitor,omitempty"`
}

// ServiceObservabilitySpec defines the desired state of ServiceObservability.
// This is the Kubernetes-native encoding of the obs-manifest-schema
// ServiceObservability manifest teams already commit to their service repos.
type ServiceObservabilitySpec struct {
	ServiceType ServiceType `json:"serviceType"`

	// environment must match the name of an ObservabilityEnvironment
	// object; that object supplies the Grafana instance, Prometheus rule
	// namespace, and catalog endpoint this service is onboarded into.
	// +kubebuilder:validation:Enum=dev;staging;prod
	Environment string `json:"environment"`

	SLO           SLOSpec           `json:"slo"`
	GoldenSignals GoldenSignalsSpec `json:"goldenSignals"`
	Dashboards    DashboardsSpec    `json:"dashboards"`
	Alerting      AlertingSpec      `json:"alerting"`
	Ownership     OwnershipSpec     `json:"ownership"`

	// scrapeConfig, if set, causes the operator to generate a
	// ServiceMonitor (or PodMonitor) so Prometheus starts scraping this
	// service automatically.
	// +optional
	ScrapeConfig *ScrapeConfigSpec `json:"scrapeConfig,omitempty"`
}

// ServiceObservabilityStatus defines the observed state of ServiceObservability.
type ServiceObservabilityStatus struct {
	// conditions report the outcome of each reconcile aspect independently:
	// Ready, DashboardSynced, AlertsSynced, ServiceMonitorSynced, CatalogSynced.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration is the .metadata.generation the status was last
	// computed from; used to tell a stale status from a current one.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// dashboardUID is the Grafana dashboard UID once published.
	// +optional
	DashboardUID string `json:"dashboardUID,omitempty"`

	// dashboardURL is the browsable Grafana dashboard URL.
	// +optional
	DashboardURL string `json:"dashboardURL,omitempty"`

	// lastReconcileTime is when the reconciler last ran to completion
	// (successfully or not).
	// +optional
	LastReconcileTime metav1.Time `json:"lastReconcileTime,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Service Type",type=string,JSONPath=`.spec.serviceType`
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ServiceObservability is the Schema for the serviceobservabilities API
type ServiceObservability struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ServiceObservability
	// +required
	Spec ServiceObservabilitySpec `json:"spec"`

	// status defines the observed state of ServiceObservability
	// +optional
	Status ServiceObservabilityStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ServiceObservabilityList contains a list of ServiceObservability
type ServiceObservabilityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ServiceObservability `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ServiceObservability{}, &ServiceObservabilityList{})
		return nil
	})
}
