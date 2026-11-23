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

package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
	"github.com/acme/obs-onboarding-controller/internal/catalog"
	"github.com/acme/obs-onboarding-controller/internal/catalogapi"
	"github.com/acme/obs-onboarding-controller/internal/gentools"
)

var testCounter int

// fakeGrafana is a real, runnable httptest server implementing just enough
// of the Grafana HTTP API for these tests -- not a mocked client. When
// fail is true, it returns 500 for every request (the "Grafana API 4xx/5xx"
// scenario from the requirements).
func fakeGrafana() (*httptest.Server, *bool, func() []string) {
	var mu sync.Mutex
	var calls []string
	fail := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()

		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "internal error"})
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/folders/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/folders":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"uid": "team-checkout", "title": "team-checkout"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/dashboards/db":
			var body struct {
				Dashboard map[string]any `json:"dashboard"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			uid, _ := body.Dashboard["uid"].(string)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"uid": uid, "url": "/d/" + uid + "/dashboard", "version": 1, "status": "success",
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/dashboards/uid/"):
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv, &fail, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(calls))
		copy(out, calls)
		return out
	}
}

var _ = Describe("ServiceObservability reconciliation", func() {
	var (
		namespace    string
		envName      string
		grafanaSrv   *httptest.Server
		grafanaFail  *bool
		grafanaCalls func() []string
		catalogStore catalog.Store
		catalogSrv   *httptest.Server
		reconciler   *ServiceObservabilityReconciler
	)

	BeforeEach(func() {
		testCounter++
		namespace = fmt.Sprintf("test-ns-%d", testCounter)
		// spec.environment is CRD-validated to dev/staging/prod, so the
		// ObservabilityEnvironment name (which the reconciler looks up by
		// that value) must be one of those too; sequential test execution
		// plus per-test namespaces/catalog stores keep this from colliding.
		envName = "prod"

		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())

		grafanaSrv, grafanaFail, grafanaCalls = fakeGrafana()

		catalogStore = catalog.NewMemStore()
		catalogSrv = httptest.NewServer(catalog.NewServer(catalogStore).Handler())

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "grafana-token", Namespace: namespace},
			StringData: map[string]string{"token": "test-token"},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		env := &obsv1.ObservabilityEnvironment{
			ObjectMeta: metav1.ObjectMeta{Name: envName},
			Spec: obsv1.ObservabilityEnvironmentSpec{
				Grafana: obsv1.GrafanaSpec{
					URL:               grafanaSrv.URL,
					FolderPrefix:      "team-",
					APITokenSecretRef: obsv1.SecretKeyRef{Name: "grafana-token", Namespace: namespace, Key: "token"},
				},
				Prometheus: obsv1.PrometheusSpec{
					RuleNamespace:    namespace,
					AdditionalLabels: map[string]string{"release": "kube-prometheus-stack"},
				},
				Catalog: obsv1.CatalogSpec{URL: catalogSrv.URL},
			},
		}
		Expect(k8sClient.Create(ctx, env)).To(Succeed())

		reconciler = &ServiceObservabilityReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Renderer: &gentools.Renderer{GenDashboardBin: genDashboardBin, GenAlertsBin: genAlertsBin, Timeout: 30 * time.Second},
		}
	})

	AfterEach(func() {
		grafanaSrv.Close()
		catalogSrv.Close()
		_ = k8sClient.Delete(ctx, &obsv1.ObservabilityEnvironment{ObjectMeta: metav1.ObjectMeta{Name: envName}})
		_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
	})

	newServiceObservability := func(name string) *obsv1.ServiceObservability {
		return &obsv1.ServiceObservability{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: obsv1.ServiceObservabilitySpec{
				ServiceType: obsv1.ServiceTypeHTTP,
				Environment: envName,
				SLO:         obsv1.SLOSpec{AvailabilityTarget: 0.999, LatencyTargetMS: 300, LatencyPercentile: 99},
				GoldenSignals: obsv1.GoldenSignalsSpec{
					TrafficMetric:     "http_server_requests_total",
					ErrorMetric:       "http_server_request_errors_total",
					LatencyMetric:     "http_server_request_duration_seconds",
					SaturationMetrics: []string{"cpu_utilization_ratio"},
				},
				Dashboards: obsv1.DashboardsSpec{Template: "http-service-v2"},
				Alerting:   obsv1.AlertingSpec{BurnRateAlerts: true},
				Ownership: obsv1.OwnershipSpec{
					Team:             "team-checkout",
					SlackChannel:     "#checkout-alerts",
					PagerDutyService: "checkout-production",
				},
			},
		}
	}

	// reconcileTwice mirrors what the manager's workqueue actually does:
	// the first pass only adds the finalizer (and requeues), the second
	// pass runs the real sync.
	reconcileTwice := func(key types.NamespacedName) (reconcile.Result, error) {
		if _, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			return reconcile.Result{}, err
		}
		return reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	}

	It("happy path: dashboard, alerts, monitor and catalog all sync", func() {
		svc := newServiceObservability("checkout-api")
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		key := types.NamespacedName{Name: svc.Name, Namespace: namespace}

		_, err := reconcileTwice(key)
		Expect(err).NotTo(HaveOccurred())

		var updated obsv1.ServiceObservability
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeReady)).To(Equal(metav1.ConditionTrue))
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeDashboardSynced)).To(Equal(metav1.ConditionTrue))
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeAlertsSynced)).To(Equal(metav1.ConditionTrue))
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeCatalogSynced)).To(Equal(metav1.ConditionTrue))
		Expect(updated.Status.DashboardUID).To(Equal("checkout-api-" + envName))

		var rule monitoringv1.PrometheusRule
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "checkout-api-slo-alerts", Namespace: namespace}, &rule)).To(Succeed())
		Expect(rule.Spec.Groups).NotTo(BeEmpty())
		Expect(rule.Labels).To(HaveKeyWithValue("release", "kube-prometheus-stack"))

		// obs-developer-portal's Health Overview page depends on these
		// two recording rules existing for every service (see
		// internal/promsync/recordingrules.go) -- assert they actually
		// made it into the applied PrometheusRule, not just that the
		// builder function produces them in isolation.
		var recordingGroup *monitoringv1.RuleGroup
		for i := range rule.Spec.Groups {
			if rule.Spec.Groups[i].Name == "checkout-api.slo.recording" {
				recordingGroup = &rule.Spec.Groups[i]
			}
		}
		Expect(recordingGroup).NotTo(BeNil(), "expected a checkout-api.slo.recording group in the applied PrometheusRule")
		recordNames := make([]string, len(recordingGroup.Rules))
		for i, r := range recordingGroup.Rules {
			recordNames[i] = r.Record
		}
		Expect(recordNames).To(ConsistOf("obs:slo_error_budget_remaining:percent", "obs:slo_burn_rate:ratio1h"))

		rec, ok := catalogStore.Get(envName, "checkout-api")
		Expect(ok).To(BeTrue())
		Expect(rec.Status).To(Equal(catalogapi.StatusActive))
		Expect(rec.Team).To(Equal("team-checkout"))
		// The portal's real (non-demo) catalog integration needs these
		// fields to compute error budgets and build PromQL -- see
		// internal/catalogapi.ServiceRecord's doc comment.
		Expect(rec.AvailabilityTarget).To(Equal(0.999))
		Expect(rec.TrafficMetric).To(Equal("http_server_requests_total"))
		Expect(rec.ErrorMetric).To(Equal("http_server_request_errors_total"))
		Expect(rec.LatencyMetric).To(Equal("http_server_request_duration_seconds"))

		Expect(grafanaCalls()).To(ContainElement("POST /api/dashboards/db"))
	})

	It("surfaces Grafana 5xx errors on DashboardSynced without blocking the other aspects", func() {
		*grafanaFail = true
		svc := newServiceObservability("checkout-api-degraded")
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		key := types.NamespacedName{Name: svc.Name, Namespace: namespace}

		_, err := reconcileTwice(key)
		Expect(err).To(HaveOccurred())

		var updated obsv1.ServiceObservability
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeDashboardSynced)).To(Equal(metav1.ConditionFalse))
		Expect(conditionReason(updated.Status.Conditions, obsv1.ConditionTypeDashboardSynced)).To(Equal("GrafanaTemporarilyUnavailable"))
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeReady)).To(Equal(metav1.ConditionFalse))

		// The PrometheusRule and catalog record are independent of Grafana
		// and should still have synced.
		var rule monitoringv1.PrometheusRule
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "checkout-api-degraded-slo-alerts", Namespace: namespace}, &rule)).To(Succeed())
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeAlertsSynced)).To(Equal(metav1.ConditionTrue))
		_, ok := catalogStore.Get(envName, "checkout-api-degraded")
		Expect(ok).To(BeTrue())

		By("recovering once Grafana comes back")
		*grafanaFail = false
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		Expect(conditionStatus(updated.Status.Conditions, obsv1.ConditionTypeReady)).To(Equal(metav1.ConditionTrue))
	})

	It("on delete: removes the dashboard and PrometheusRule and decommissions the catalog record", func() {
		svc := newServiceObservability("checkout-api-delete")
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		key := types.NamespacedName{Name: svc.Name, Namespace: namespace}
		_, err := reconcileTwice(key)
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Delete(ctx, svc)).To(Succeed())
		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		var gone obsv1.ServiceObservability
		err = k8sClient.Get(ctx, key, &gone)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "object should be gone once the finalizer released it")

		var rule monitoringv1.PrometheusRule
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "checkout-api-delete-slo-alerts", Namespace: namespace}, &rule)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "PrometheusRule should have been deleted")

		rec, ok := catalogStore.Get(envName, "checkout-api-delete")
		Expect(ok).To(BeTrue(), "catalog record must NOT be hard-deleted, only decommissioned")
		Expect(rec.Status).To(Equal(catalogapi.StatusDecommissioned))

		Expect(grafanaCalls()).To(ContainElement("DELETE /api/dashboards/uid/checkout-api-delete-" + envName))
	})
})

func conditionStatus(conds []metav1.Condition, condType string) metav1.ConditionStatus {
	for _, c := range conds {
		if c.Type == condType {
			return c.Status
		}
	}
	return ""
}

func conditionReason(conds []metav1.Condition, condType string) string {
	for _, c := range conds {
		if c.Type == condType {
			return c.Reason
		}
	}
	return ""
}
