// Package envresolve reads the Secrets an ObservabilityEnvironment
// references. It is the operator's entire "secret manager integration":
// whether a key came from a person's `kubectl create secret`, an External
// Secrets Operator ExternalSecret synced from Vault, or anything else, this
// package (and the operator generally) only ever sees a normal core/v1
// Secret through the Kubernetes API. See README.md.
package envresolve

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
)

// SecretKey resolves a SecretKeyRef to its string value.
func SecretKey(ctx context.Context, c client.Client, ref obsv1.SecretKeyRef, defaultNamespace string) (string, error) {
	namespace := ref.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, &secret); err != nil {
		return "", fmt.Errorf("get secret %s/%s: %w", namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("secret %s/%s has no key %q", namespace, ref.Name, ref.Key)
	}
	return string(value), nil
}
