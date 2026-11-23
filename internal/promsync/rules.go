// Package promsync turns gen-alerts' rendered PrometheusRule YAML into a
// live Kubernetes object, and builds ServiceMonitor/PodMonitor objects
// directly from a ServiceObservability spec. Applying these objects is all
// this operator does to make Prometheus Operator pick them up -- it never
// talks to Prometheus or Alertmanager directly.
package promsync

import (
	"context"
	"fmt"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
)

// ParsePrometheusRule decodes gen-alerts' YAML output into a typed object.
func ParsePrometheusRule(raw []byte) (*monitoringv1.PrometheusRule, error) {
	var rule monitoringv1.PrometheusRule
	if err := yaml.Unmarshal(raw, &rule); err != nil {
		return nil, fmt.Errorf("decode PrometheusRule: %w", err)
	}
	return &rule, nil
}

// ApplyPrometheusRule creates or updates a PrometheusRule named name in
// namespace, owned by owner (so it is garbage-collected if the
// ServiceObservability is ever deleted without going through the finalizer
// path), merging additionalLabels from the ObservabilityEnvironment on top
// of what gen-alerts produced (e.g. the label a kube-prometheus-stack
// ruleSelector matches on).
func ApplyPrometheusRule(ctx context.Context, c client.Client, scheme *runtime.Scheme, namespace, name string, owner metav1.Object, rendered *monitoringv1.PrometheusRule, additionalLabels map[string]string) error {
	obj := &monitoringv1.PrometheusRule{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, c, obj, func() error {
		obj.Labels = mergeLabels(rendered.Labels, additionalLabels)
		obj.Spec = rendered.Spec
		if owner == nil {
			return nil
		}
		return controllerutil.SetControllerReference(owner, obj, scheme)
	})
	if err != nil {
		return fmt.Errorf("apply PrometheusRule %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeletePrometheusRule removes a PrometheusRule if it exists.
func DeletePrometheusRule(ctx context.Context, c client.Client, namespace, name string) error {
	obj := &monitoringv1.PrometheusRule{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := c.Delete(ctx, obj); err != nil && !isNotFound(err) {
		return fmt.Errorf("delete PrometheusRule %s/%s: %w", namespace, name, err)
	}
	return nil
}

// BuildServiceMonitor/BuildPodMonitor construct (but do not apply) a
// ServiceMonitor/PodMonitor from spec.ScrapeConfig.

func BuildServiceMonitor(namespace, name string, cfg obsv1.ScrapeConfigSpec, additionalLabels map[string]string) *monitoringv1.ServiceMonitor {
	return &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: additionalLabels},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: cfg.Selector,
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:     cfg.Port,
					Path:     nonEmpty(cfg.Path, "/metrics"),
					Interval: monitoringv1.Duration(nonEmpty(cfg.Interval, "30s")),
				},
			},
		},
	}
}

func BuildPodMonitor(namespace, name string, cfg obsv1.ScrapeConfigSpec, additionalLabels map[string]string) *monitoringv1.PodMonitor {
	port := cfg.Port
	return &monitoringv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: additionalLabels},
		Spec: monitoringv1.PodMonitorSpec{
			Selector: cfg.Selector,
			PodMetricsEndpoints: []monitoringv1.PodMetricsEndpoint{
				{
					Port:     &port,
					Path:     nonEmpty(cfg.Path, "/metrics"),
					Interval: monitoringv1.Duration(nonEmpty(cfg.Interval, "30s")),
				},
			},
		},
	}
}

// ApplyServiceMonitor / ApplyPodMonitor / DeleteMonitor mirror the
// PrometheusRule create-or-update / delete helpers above.

func ApplyServiceMonitor(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner metav1.Object, desired *monitoringv1.ServiceMonitor) error {
	obj := &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, c, obj, func() error {
		obj.Labels = desired.Labels
		obj.Spec = desired.Spec
		return controllerutil.SetControllerReference(owner, obj, scheme)
	})
	if err != nil {
		return fmt.Errorf("apply ServiceMonitor %s/%s: %w", desired.Namespace, desired.Name, err)
	}
	return nil
}

func ApplyPodMonitor(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner metav1.Object, desired *monitoringv1.PodMonitor) error {
	obj := &monitoringv1.PodMonitor{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, c, obj, func() error {
		obj.Labels = desired.Labels
		obj.Spec = desired.Spec
		return controllerutil.SetControllerReference(owner, obj, scheme)
	})
	if err != nil {
		return fmt.Errorf("apply PodMonitor %s/%s: %w", desired.Namespace, desired.Name, err)
	}
	return nil
}

func DeleteServiceMonitor(ctx context.Context, c client.Client, namespace, name string) error {
	obj := &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := c.Delete(ctx, obj); err != nil && !isNotFound(err) {
		return fmt.Errorf("delete ServiceMonitor %s/%s: %w", namespace, name, err)
	}
	return nil
}

func DeletePodMonitor(ctx context.Context, c client.Client, namespace, name string) error {
	obj := &monitoringv1.PodMonitor{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := c.Delete(ctx, obj); err != nil && !isNotFound(err) {
		return fmt.Errorf("delete PodMonitor %s/%s: %w", namespace, name, err)
	}
	return nil
}

func mergeLabels(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func nonEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func isNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}
