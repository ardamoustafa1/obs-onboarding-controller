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

// SecretKeyRef points at a single key of a Secret. Whatever wrote that key
// -- a person, kubectl, or an External Secrets Operator ExternalSecret
// syncing from Vault -- is invisible to this operator: it only ever reads
// a Secret object through the normal Kubernetes API. This is the whole of
// this repo's Vault/External-Secrets integration story; see README.md.
type SecretKeyRef struct {
	// name of the Secret, in the same namespace the operator runs in
	// unless namespace is set.
	Name string `json:"name"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// key within the Secret's data.
	Key string `json:"key"`
}

// GrafanaSpec configures the Grafana instance this environment publishes
// dashboards to.
type GrafanaSpec struct {
	// url is the Grafana base URL, e.g. https://grafana.prod.acme.internal.
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`

	// apiTokenSecretRef points at a Secret key holding a Grafana service
	// account token with dashboard:write and folders:write scope.
	APITokenSecretRef SecretKeyRef `json:"apiTokenSecretRef"`

	// folderPrefix namespaces Grafana folders per team, e.g. "team-" turns
	// team "checkout" into folder UID "team-checkout" -- kept consistent
	// with the RBAC repo's folder-per-team convention.
	// +kubebuilder:default="team-"
	FolderPrefix string `json:"folderPrefix,omitempty"`
}

// PrometheusSpec configures where generated PrometheusRule/ServiceMonitor
// objects land and how Prometheus Operator is expected to discover them.
type PrometheusSpec struct {
	// ruleNamespace is where PrometheusRule/ServiceMonitor objects are
	// created. Defaults to the ServiceObservability's own namespace when
	// empty.
	// +optional
	RuleNamespace string `json:"ruleNamespace,omitempty"`

	// additionalLabels are stamped onto every generated PrometheusRule and
	// ServiceMonitor so the environment's Prometheus Operator
	// ruleSelector/serviceMonitorSelector picks them up, e.g.
	// {"release": "kube-prometheus-stack"}.
	// +optional
	AdditionalLabels map[string]string `json:"additionalLabels,omitempty"`
}

// CatalogSpec configures the service-catalog REST API this environment
// registers services with (see cmd/catalog).
type CatalogSpec struct {
	// url is the catalog API base URL, e.g. http://obs-catalog.obs-system.svc:8080.
	URL string `json:"url"`

	// apiTokenSecretRef optionally authenticates catalog writes.
	// +optional
	APITokenSecretRef *SecretKeyRef `json:"apiTokenSecretRef,omitempty"`
}

// ObservabilityEnvironmentSpec is the "environment overlay": the same
// ServiceObservability manifest is applied unchanged across dev/staging/prod
// (and, by deploying one operator instance per cluster, across clusters);
// what differs per environment -- which Grafana, which Prometheus, which
// catalog, which credentials -- lives here instead of being duplicated into
// every service manifest.
type ObservabilityEnvironmentSpec struct {
	// displayName is a human-friendly label, purely cosmetic.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	Grafana    GrafanaSpec    `json:"grafana"`
	Prometheus PrometheusSpec `json:"prometheus"`
	Catalog    CatalogSpec    `json:"catalog"`
}

// ObservabilityEnvironmentStatus defines the observed state of ObservabilityEnvironment.
type ObservabilityEnvironmentStatus struct {
	// conditions reports whether the referenced secrets currently resolve
	// and the Grafana/catalog endpoints are reachable.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Grafana",type=string,JSONPath=`.spec.grafana.url`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// ObservabilityEnvironment is the Schema for the observabilityenvironments API.
// Its name (metadata.name) is the value ServiceObservability.spec.environment
// references, e.g. "dev", "staging", "prod".
type ObservabilityEnvironment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ObservabilityEnvironment
	// +required
	Spec ObservabilityEnvironmentSpec `json:"spec"`

	// status defines the observed state of ObservabilityEnvironment
	// +optional
	Status ObservabilityEnvironmentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ObservabilityEnvironmentList contains a list of ObservabilityEnvironment
type ObservabilityEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ObservabilityEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ObservabilityEnvironment{}, &ObservabilityEnvironmentList{})
		return nil
	})
}
