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
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client

	// genDashboardBin / genAlertsBin point at real gen-dashboard/gen-alerts
	// binaries built from the sibling repos for this test run (see
	// buildSiblingCLI below). Tests exercise the actual rendering pipeline
	// through internal/gentools, not a stub.
	genDashboardBin string
	genAlertsBin    string
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = obsv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = monitoringv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("building the real gen-dashboard and gen-alerts CLIs from the sibling repos")
	binDir := GinkgoT().TempDir()
	genDashboardBin = buildSiblingCLI(binDir, "obs-golden-signal-templates", "gen-dashboard")
	genAlertsBin = buildSiblingCLI(binDir, "obs-alert-rule-templates", "gen-alerts")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			// Vendored upstream Prometheus Operator CRDs (PrometheusRule,
			// ServiceMonitor, PodMonitor) -- envtest only ships core
			// Kubernetes API types, not third-party operator CRDs, and
			// this suite needs the real schema to validate what the
			// reconciler applies. See config/crd/vendor/README.md.
			filepath.Join("..", "..", "config", "crd", "vendor"),
		},
		ErrorIfCRDPathMissing: true,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// buildSiblingCLI compiles cmd/<binName> from the sibling repo at
// ../../../<repoName> (obs-onboarding-controller, obs-golden-signal-templates,
// and obs-alert-rule-templates are checked out side by side -- see the
// top-level README) into outDir, returning the built binary's path.
//
// This is deliberately a real `go build` of the real CLI, not a fake: the
// whole point of these tests is exercising internal/gentools against the
// actual tools other teams run, the same way the operator's container
// image bakes in binaries built the same way (see Dockerfile).
func buildSiblingCLI(outDir, repoName, binName string) string {
	repoPath, err := filepath.Abs(filepath.Join("..", "..", "..", repoName))
	Expect(err).NotTo(HaveOccurred())
	_, err = os.Stat(repoPath)
	Expect(err).NotTo(HaveOccurred(), "expected sibling repo at %s -- check out obs-golden-signal-templates, obs-alert-rule-templates and obs-onboarding-controller side by side", repoPath)

	outPath := filepath.Join(outDir, binName)
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/"+binName)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "building %s: %s", binName, string(output))
	return outPath
}

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
