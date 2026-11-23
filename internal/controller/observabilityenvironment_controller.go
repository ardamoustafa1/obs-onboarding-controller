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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
	"github.com/acme/obs-onboarding-controller/internal/envresolve"
)

// ObservabilityEnvironmentReconciler does not talk to Grafana or the
// catalog itself; it only validates that the secrets an environment
// references actually resolve, so a broken environment overlay (a typo'd
// Secret name, a token an External Secrets Operator has not synced yet)
// surfaces on the ObservabilityEnvironment object immediately instead of
// only showing up indirectly on every ServiceObservability that references it.
type ObservabilityEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=obs.acme.com,resources=observabilityenvironments,verbs=get;list;watch
// +kubebuilder:rbac:groups=obs.acme.com,resources=observabilityenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ObservabilityEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var env obsv1.ObservabilityEnvironment
	if err := r.Get(ctx, req.NamespacedName, &env); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if _, err := envresolve.SecretKey(ctx, r.Client, env.Spec.Grafana.APITokenSecretRef, env.Namespace); err != nil {
		setEnvCondition(&env, "Ready", metav1.ConditionFalse, "GrafanaSecretUnresolved", err.Error())
		_ = r.Status().Update(ctx, &env)
		return ctrl.Result{}, err
	}

	if ref := env.Spec.Catalog.APITokenSecretRef; ref != nil {
		if _, err := envresolve.SecretKey(ctx, r.Client, *ref, env.Namespace); err != nil {
			setEnvCondition(&env, "Ready", metav1.ConditionFalse, "CatalogSecretUnresolved", err.Error())
			_ = r.Status().Update(ctx, &env)
			return ctrl.Result{}, err
		}
	}

	setEnvCondition(&env, "Ready", metav1.ConditionTrue, "SecretsResolved", "grafana and catalog credentials resolve")
	env.Status.ObservedGeneration = env.Generation
	if err := r.Status().Update(ctx, &env); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func setEnvCondition(env *obsv1.ObservabilityEnvironment, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: env.Generation,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ObservabilityEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&obsv1.ObservabilityEnvironment{}).
		Named("observabilityenvironment").
		Complete(r)
}
