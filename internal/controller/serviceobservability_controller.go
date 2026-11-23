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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
	"github.com/acme/obs-onboarding-controller/internal/catalogapi"
	"github.com/acme/obs-onboarding-controller/internal/catalogclient"
	"github.com/acme/obs-onboarding-controller/internal/envresolve"
	"github.com/acme/obs-onboarding-controller/internal/gentools"
	"github.com/acme/obs-onboarding-controller/internal/grafana"
	"github.com/acme/obs-onboarding-controller/internal/manifestbuild"
	"github.com/acme/obs-onboarding-controller/internal/promsync"
)

// finalizerName gates deletion until Grafana/PrometheusRule/catalog cleanup
// has actually run (step 2g of the requirements: decommission, not hard
// delete).
const finalizerName = "obs.acme.com/onboarding-controller"

// driftCheckInterval is how often the reconciler re-runs even when nothing
// changed the CR, to catch and correct drift (someone hand-edited the
// Grafana dashboard, deleted the PrometheusRule, ...). See requirement 3.
const driftCheckInterval = 5 * time.Minute

// ServiceObservabilityReconciler reconciles a ServiceObservability object.
//
// State machine (see README.md for the diagram):
//
//	Get CR
//	  |-- not found -----------------------------------------> done
//	  |-- DeletionTimestamp set -------------------------------> reconcileDelete
//	  |-- finalizer missing -> add it, requeue
//	  v
//	Look up ObservabilityEnvironment (spec.environment)
//	  |-- not found/misconfigured -> Ready=False, requeue with backoff
//	  v
//	Build obs-manifest-schema YAML from spec
//	  v
//	syncDashboard   (gen-dashboard CLI -> Grafana HTTP API)      \
//	syncAlerts      (gen-alerts CLI -> apply PrometheusRule)      | independent aspects,
//	syncMonitor     (build + apply ServiceMonitor/PodMonitor)     | each sets its own Condition,
//	syncCatalog     (HTTP upsert to the catalog component)       /  a failure in one does not block the others
//	  v
//	Ready = AND of the four conditions; Status().Update()
//	  |-- any aspect failed -> return the error (client-go workqueue retries with exponential backoff)
//	  v
//	RequeueAfter driftCheckInterval
type ServiceObservabilityReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Renderer *gentools.Renderer
}

// +kubebuilder:rbac:groups=obs.acme.com,resources=serviceobservabilities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=obs.acme.com,resources=serviceobservabilities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=obs.acme.com,resources=serviceobservabilities/finalizers,verbs=update
// +kubebuilder:rbac:groups=obs.acme.com,resources=observabilityenvironments,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=prometheusrules;servicemonitors;podmonitors,verbs=get;list;watch;create;update;patch;delete

func (r *ServiceObservabilityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	start := time.Now()
	outcome := "success"
	defer func() { reconcileDuration.WithLabelValues(outcome).Observe(time.Since(start).Seconds()) }()

	var svcObs obsv1.ServiceObservability
	if err := r.Get(ctx, req.NamespacedName, &svcObs); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		outcome = "error"
		return ctrl.Result{}, err
	}

	if !svcObs.DeletionTimestamp.IsZero() {
		res, err := r.reconcileDelete(ctx, &svcObs)
		if err != nil {
			outcome = "error"
		}
		return res, err
	}

	if !controllerutil.ContainsFinalizer(&svcObs, finalizerName) {
		controllerutil.AddFinalizer(&svcObs, finalizerName)
		if err := r.Update(ctx, &svcObs); err != nil {
			outcome = "error"
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	env, err := r.getEnvironment(ctx, svcObs.Spec.Environment)
	if err != nil {
		reconcileErrorsTotal.WithLabelValues("environment").Inc()
		setCondition(&svcObs, obsv1.ConditionTypeReady, metav1.ConditionFalse, "EnvironmentUnresolved", err.Error())
		_ = r.Status().Update(ctx, &svcObs)
		outcome = "error"
		return ctrl.Result{}, err
	}

	manifestYAML, err := manifestbuild.Build(svcObs.Name, svcObs.Spec)
	if err != nil {
		reconcileErrorsTotal.WithLabelValues("manifest").Inc()
		setCondition(&svcObs, obsv1.ConditionTypeReady, metav1.ConditionFalse, "ManifestBuildFailed", err.Error())
		_ = r.Status().Update(ctx, &svcObs)
		outcome = "error"
		return ctrl.Result{}, err
	}

	var aggregateErr error
	if err := r.syncDashboard(ctx, &svcObs, env, manifestYAML); err != nil {
		log.Error(err, "dashboard sync failed")
		reconcileErrorsTotal.WithLabelValues("dashboard").Inc()
		aggregateErr = errors.Join(aggregateErr, err)
	}
	if err := r.syncAlerts(ctx, &svcObs, env, manifestYAML); err != nil {
		log.Error(err, "alert rule sync failed")
		reconcileErrorsTotal.WithLabelValues("alerts").Inc()
		aggregateErr = errors.Join(aggregateErr, err)
	}
	if err := r.syncMonitor(ctx, &svcObs, env); err != nil {
		log.Error(err, "service/pod monitor sync failed")
		reconcileErrorsTotal.WithLabelValues("monitor").Inc()
		aggregateErr = errors.Join(aggregateErr, err)
	}
	if err := r.syncCatalog(ctx, &svcObs, env); err != nil {
		log.Error(err, "catalog sync failed")
		reconcileErrorsTotal.WithLabelValues("catalog").Inc()
		aggregateErr = errors.Join(aggregateErr, err)
	}

	if aggregateErr != nil {
		setCondition(&svcObs, obsv1.ConditionTypeReady, metav1.ConditionFalse, "SyncFailed", aggregateErr.Error())
	} else {
		setCondition(&svcObs, obsv1.ConditionTypeReady, metav1.ConditionTrue, "Synced", "dashboard, alerts, monitor and catalog are in sync")
	}
	syncedServices.WithLabelValues(svcObs.Namespace, svcObs.Name, svcObs.Spec.Environment).Set(boolToFloat(aggregateErr == nil))

	svcObs.Status.ObservedGeneration = svcObs.Generation
	svcObs.Status.LastReconcileTime = metav1.Now()
	if err := r.Status().Update(ctx, &svcObs); err != nil {
		outcome = "error"
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}

	if aggregateErr != nil {
		// Returning the error hands retry pacing to controller-runtime's
		// workqueue, which already backs off exponentially between
		// attempts -- see README.md's "hata durumları" section.
		outcome = "error"
		return ctrl.Result{}, aggregateErr
	}
	return ctrl.Result{RequeueAfter: driftCheckInterval}, nil
}

func (r *ServiceObservabilityReconciler) reconcileDelete(ctx context.Context, svcObs *obsv1.ServiceObservability) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(svcObs, finalizerName) {
		return ctrl.Result{}, nil
	}

	env, err := r.getEnvironment(ctx, svcObs.Spec.Environment)
	switch {
	case err == nil:
		if cleanupErr := r.cleanup(ctx, svcObs, env); cleanupErr != nil {
			return ctrl.Result{}, cleanupErr
		}
	case apierrors.IsNotFound(err):
		// The environment itself is gone, so there is nothing left to
		// call Grafana/catalog through; log and proceed so the CR is not
		// stuck forever on an environment that no longer exists.
		logf.FromContext(ctx).Info("observability environment gone; skipping external cleanup", "environment", svcObs.Spec.Environment)
	default:
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(svcObs, finalizerName)
	if err := r.Update(ctx, svcObs); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *ServiceObservabilityReconciler) cleanup(ctx context.Context, svcObs *obsv1.ServiceObservability, env *obsv1.ObservabilityEnvironment) error {
	grafanaClient, err := r.grafanaClientFor(ctx, env)
	if err != nil {
		return fmt.Errorf("resolve grafana client: %w", err)
	}
	if err := grafanaClient.DeleteDashboard(ctx, dashboardUID(svcObs)); err != nil {
		return fmt.Errorf("delete dashboard: %w", err)
	}

	ns := ruleNamespace(env, svcObs.Namespace)
	if err := promsync.DeletePrometheusRule(ctx, r.Client, ns, ruleName(svcObs)); err != nil {
		return err
	}

	if svcObs.Spec.ScrapeConfig != nil {
		monitorName := svcObs.Name + "-monitor"
		if svcObs.Spec.ScrapeConfig.PodMonitor {
			if err := promsync.DeletePodMonitor(ctx, r.Client, svcObs.Namespace, monitorName); err != nil {
				return err
			}
		} else if err := promsync.DeleteServiceMonitor(ctx, r.Client, svcObs.Namespace, monitorName); err != nil {
			return err
		}
	}

	catalogClient, err := r.catalogClientFor(ctx, env)
	if err != nil {
		return fmt.Errorf("resolve catalog client: %w", err)
	}
	if err := catalogClient.Decommission(ctx, svcObs.Spec.Environment, svcObs.Name); err != nil {
		return fmt.Errorf("decommission catalog record: %w", err)
	}
	return nil
}

func (r *ServiceObservabilityReconciler) syncDashboard(ctx context.Context, svcObs *obsv1.ServiceObservability, env *obsv1.ObservabilityEnvironment, manifestYAML []byte) error {
	raw, err := r.Renderer.RenderDashboard(ctx, manifestYAML)
	if err != nil {
		setCondition(svcObs, obsv1.ConditionTypeDashboardSynced, metav1.ConditionFalse, "RenderFailed", err.Error())
		return fmt.Errorf("render dashboard: %w", err)
	}

	var artifact struct {
		Folder struct {
			UID   string `json:"uid"`
			Title string `json:"title"`
		} `json:"folder"`
		Dashboard map[string]any `json:"dashboard"`
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		setCondition(svcObs, obsv1.ConditionTypeDashboardSynced, metav1.ConditionFalse, "DecodeFailed", err.Error())
		return fmt.Errorf("decode dashboard artifact: %w", err)
	}
	// Pin a stable UID: without this Grafana would mint a new random UID
	// on every reconcile, breaking idempotency (see internal/grafana).
	artifact.Dashboard["uid"] = dashboardUID(svcObs)

	grafanaClient, err := r.grafanaClientFor(ctx, env)
	if err != nil {
		setCondition(svcObs, obsv1.ConditionTypeDashboardSynced, metav1.ConditionFalse, "ConfigError", err.Error())
		return err
	}
	if err := grafanaClient.EnsureFolder(ctx, artifact.Folder.UID, artifact.Folder.Title); err != nil {
		setCondition(svcObs, obsv1.ConditionTypeDashboardSynced, metav1.ConditionFalse, grafanaReason(err), err.Error())
		return fmt.Errorf("ensure grafana folder: %w", err)
	}
	result, err := grafanaClient.UpsertDashboard(ctx, artifact.Folder.UID, artifact.Dashboard)
	if err != nil {
		setCondition(svcObs, obsv1.ConditionTypeDashboardSynced, metav1.ConditionFalse, grafanaReason(err), err.Error())
		return fmt.Errorf("upsert grafana dashboard: %w", err)
	}

	svcObs.Status.DashboardUID = result.UID
	svcObs.Status.DashboardURL = env.Spec.Grafana.URL + result.URL
	setCondition(svcObs, obsv1.ConditionTypeDashboardSynced, metav1.ConditionTrue, "Synced", "dashboard upserted to "+svcObs.Status.DashboardURL)
	return nil
}

func (r *ServiceObservabilityReconciler) syncAlerts(ctx context.Context, svcObs *obsv1.ServiceObservability, env *obsv1.ObservabilityEnvironment, manifestYAML []byte) error {
	raw, err := r.Renderer.RenderAlerts(ctx, manifestYAML, nil)
	if err != nil {
		setCondition(svcObs, obsv1.ConditionTypeAlertsSynced, metav1.ConditionFalse, "RenderFailed", err.Error())
		return fmt.Errorf("render alerts: %w", err)
	}
	rule, err := promsync.ParsePrometheusRule(raw)
	if err != nil {
		setCondition(svcObs, obsv1.ConditionTypeAlertsSynced, metav1.ConditionFalse, "DecodeFailed", err.Error())
		return err
	}
	// obs-developer-portal's Health Overview page depends on these two
	// recording rules existing for every service -- see
	// internal/promsync/recordingrules.go's doc comment.
	rule.Spec.Groups = append(rule.Spec.Groups, promsync.BuildSLORecordingRuleGroup(svcObs))

	ns := ruleNamespace(env, svcObs.Namespace)
	if err := promsync.ApplyPrometheusRule(ctx, r.Client, r.Scheme, ns, ruleName(svcObs), ownerFor(svcObs, ns), rule, env.Spec.Prometheus.AdditionalLabels); err != nil {
		setCondition(svcObs, obsv1.ConditionTypeAlertsSynced, metav1.ConditionFalse, "ApplyFailed", err.Error())
		return err
	}
	setCondition(svcObs, obsv1.ConditionTypeAlertsSynced, metav1.ConditionTrue, "Synced", fmt.Sprintf("PrometheusRule %s/%s applied", ns, ruleName(svcObs)))
	return nil
}

func (r *ServiceObservabilityReconciler) syncMonitor(ctx context.Context, svcObs *obsv1.ServiceObservability, env *obsv1.ObservabilityEnvironment) error {
	if svcObs.Spec.ScrapeConfig == nil {
		setCondition(svcObs, obsv1.ConditionTypeServiceMonitorSynced, metav1.ConditionTrue, "NotRequested", "spec.scrapeConfig not set")
		return nil
	}
	cfg := *svcObs.Spec.ScrapeConfig
	name := svcObs.Name + "-monitor"

	if cfg.PodMonitor {
		desired := promsync.BuildPodMonitor(svcObs.Namespace, name, cfg, env.Spec.Prometheus.AdditionalLabels)
		if err := promsync.ApplyPodMonitor(ctx, r.Client, r.Scheme, svcObs, desired); err != nil {
			setCondition(svcObs, obsv1.ConditionTypeServiceMonitorSynced, metav1.ConditionFalse, "ApplyFailed", err.Error())
			return err
		}
	} else {
		desired := promsync.BuildServiceMonitor(svcObs.Namespace, name, cfg, env.Spec.Prometheus.AdditionalLabels)
		if err := promsync.ApplyServiceMonitor(ctx, r.Client, r.Scheme, svcObs, desired); err != nil {
			setCondition(svcObs, obsv1.ConditionTypeServiceMonitorSynced, metav1.ConditionFalse, "ApplyFailed", err.Error())
			return err
		}
	}
	setCondition(svcObs, obsv1.ConditionTypeServiceMonitorSynced, metav1.ConditionTrue, "Synced", "monitor object applied")
	return nil
}

func (r *ServiceObservabilityReconciler) syncCatalog(ctx context.Context, svcObs *obsv1.ServiceObservability, env *obsv1.ObservabilityEnvironment) error {
	catalogClient, err := r.catalogClientFor(ctx, env)
	if err != nil {
		setCondition(svcObs, obsv1.ConditionTypeCatalogSynced, metav1.ConditionFalse, "ConfigError", err.Error())
		return err
	}
	rec := catalogapi.ServiceRecord{
		Name:               svcObs.Name,
		Environment:        svcObs.Spec.Environment,
		Team:               svcObs.Spec.Ownership.Team,
		ServiceType:        string(svcObs.Spec.ServiceType),
		DashboardURL:       svcObs.Status.DashboardURL,
		SlackChannel:       svcObs.Spec.Ownership.SlackChannel,
		PagerDutyService:   svcObs.Spec.Ownership.PagerDutyService,
		Status:             catalogapi.StatusActive,
		AvailabilityTarget: svcObs.Spec.SLO.AvailabilityTarget,
		LatencyTargetMS:    svcObs.Spec.SLO.LatencyTargetMS,
		LatencyPercentile:  svcObs.Spec.SLO.LatencyPercentile,
		TrafficMetric:      svcObs.Spec.GoldenSignals.TrafficMetric,
		ErrorMetric:        svcObs.Spec.GoldenSignals.ErrorMetric,
		LatencyMetric:      latencyOrProcessingMetric(svcObs.Spec.GoldenSignals),
	}
	if err := catalogClient.Upsert(ctx, rec); err != nil {
		setCondition(svcObs, obsv1.ConditionTypeCatalogSynced, metav1.ConditionFalse, catalogReason(err), err.Error())
		return err
	}
	setCondition(svcObs, obsv1.ConditionTypeCatalogSynced, metav1.ConditionTrue, "Synced", "catalog record upserted")
	return nil
}

func (r *ServiceObservabilityReconciler) getEnvironment(ctx context.Context, name string) (*obsv1.ObservabilityEnvironment, error) {
	var env obsv1.ObservabilityEnvironment
	if err := r.Get(ctx, types.NamespacedName{Name: name}, &env); err != nil {
		return nil, fmt.Errorf("get ObservabilityEnvironment %q: %w", name, err)
	}
	return &env, nil
}

func (r *ServiceObservabilityReconciler) grafanaClientFor(ctx context.Context, env *obsv1.ObservabilityEnvironment) (*grafana.Client, error) {
	token, err := envresolve.SecretKey(ctx, r.Client, env.Spec.Grafana.APITokenSecretRef, env.Namespace)
	if err != nil {
		return nil, fmt.Errorf("resolve grafana token: %w", err)
	}
	return grafana.New(env.Spec.Grafana.URL, token), nil
}

func (r *ServiceObservabilityReconciler) catalogClientFor(ctx context.Context, env *obsv1.ObservabilityEnvironment) (*catalogclient.Client, error) {
	token := ""
	if ref := env.Spec.Catalog.APITokenSecretRef; ref != nil {
		resolved, err := envresolve.SecretKey(ctx, r.Client, *ref, env.Namespace)
		if err != nil {
			return nil, fmt.Errorf("resolve catalog token: %w", err)
		}
		token = resolved
	}
	return catalogclient.New(env.Spec.Catalog.URL, token), nil
}

// ownerFor returns svcObs as the controller-reference owner only when the
// PrometheusRule lands in the CR's own namespace: Kubernetes owner
// references cannot cross namespaces, and ObservabilityEnvironment can
// route rules into a different, cluster-shared namespace. When the
// namespaces differ, cleanup relies solely on the finalizer path instead of
// garbage collection.
func ownerFor(svcObs *obsv1.ServiceObservability, targetNamespace string) metav1.Object {
	if targetNamespace == svcObs.Namespace {
		return svcObs
	}
	return nil
}

// latencyOrProcessingMetric returns whichever of latencyMetric /
// processingDurationMetric the manifest's serviceType populates (mirrors
// obs-alert-rule-templates' internal/manifest.Manifest method of the same
// shape, kept here rather than shared since that package is internal to
// its own module).
func latencyOrProcessingMetric(gs obsv1.GoldenSignalsSpec) string {
	if gs.LatencyMetric != "" {
		return gs.LatencyMetric
	}
	return gs.ProcessingDurationMetric
}

func dashboardUID(svcObs *obsv1.ServiceObservability) string {
	uid := fmt.Sprintf("%s-%s", svcObs.Name, svcObs.Spec.Environment)
	if len(uid) > 40 {
		uid = uid[:40]
	}
	return uid
}

func ruleName(svcObs *obsv1.ServiceObservability) string {
	return svcObs.Name + "-slo-alerts"
}

func ruleNamespace(env *obsv1.ObservabilityEnvironment, fallback string) string {
	if env.Spec.Prometheus.RuleNamespace != "" {
		return env.Spec.Prometheus.RuleNamespace
	}
	return fallback
}

func setCondition(svcObs *obsv1.ServiceObservability, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&svcObs.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: svcObs.Generation,
	})
}

func grafanaReason(err error) string {
	var apiErr *grafana.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Retryable() {
			return "GrafanaTemporarilyUnavailable"
		}
		return "GrafanaRequestRejected"
	}
	return "GrafanaUnreachable"
}

func catalogReason(err error) string {
	var apiErr *catalogclient.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Retryable() {
			return "CatalogTemporarilyUnavailable"
		}
		return "CatalogRequestRejected"
	}
	return "CatalogUnreachable"
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceObservabilityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Renderer == nil {
		r.Renderer = gentools.NewFromEnv()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&obsv1.ServiceObservability{}).
		Named("serviceobservability").
		Complete(r)
}
