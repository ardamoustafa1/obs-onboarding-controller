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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
)

var _ = Describe("ObservabilityEnvironment Controller", func() {
	It("reports Ready=True once its Grafana secret resolves, and False when it does not", func() {
		testCounter++
		namespace := fmt.Sprintf("test-envctrl-ns-%d", testCounter)
		envName := fmt.Sprintf("test-envctrl-env-%d", testCounter)

		Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())

		env := &obsv1.ObservabilityEnvironment{
			ObjectMeta: metav1.ObjectMeta{Name: envName},
			Spec: obsv1.ObservabilityEnvironmentSpec{
				Grafana: obsv1.GrafanaSpec{
					URL:               "http://grafana.invalid",
					APITokenSecretRef: obsv1.SecretKeyRef{Name: "missing-secret", Namespace: namespace, Key: "token"},
				},
				Prometheus: obsv1.PrometheusSpec{RuleNamespace: namespace},
				Catalog:    obsv1.CatalogSpec{URL: "http://catalog.invalid"},
			},
		}
		Expect(k8sClient.Create(ctx, env)).To(Succeed())

		reconciler := &ObservabilityEnvironmentReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		key := types.NamespacedName{Name: envName}

		By("failing while the referenced secret does not exist")
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).To(HaveOccurred())

		var updated obsv1.ObservabilityEnvironment
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		Expect(conditionStatus(updated.Status.Conditions, "Ready")).To(Equal(metav1.ConditionFalse))
		Expect(conditionReason(updated.Status.Conditions, "Ready")).To(Equal("GrafanaSecretUnresolved"))

		By("succeeding once the secret is created")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "missing-secret", Namespace: namespace},
			StringData: map[string]string{"token": "test-token"},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, key, &updated)).To(Succeed())
		Expect(conditionStatus(updated.Status.Conditions, "Ready")).To(Equal(metav1.ConditionTrue))

		Expect(k8sClient.Delete(ctx, env)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
	})
})
