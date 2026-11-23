// Package gentools invokes the obs-golden-signal-templates (gen-dashboard)
// and obs-alert-rule-templates (gen-alerts) CLIs as subprocesses.
//
// Those tools live in their own repos/Go modules and deliberately keep their
// rendering internals under internal/ -- they are consumed as CLIs, not as
// libraries. The operator's container image is built with both binaries
// baked in (see Dockerfile); this package shells out to whatever binary
// GEN_DASHBOARD_BIN / GEN_ALERTS_BIN resolve to (defaulting to $PATH),
// which is exactly the pattern the manifest-only "container image /
// init-container / job" requirement describes, just realized as binaries
// inside the operator's own image rather than in a separate Job pod. See
// README.md ("Neden library import değil, CLI exec?") for the trade-off and
// the Job-based alternative for teams that want stronger isolation.
package gentools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const defaultTimeout = 30 * time.Second

type Renderer struct {
	GenDashboardBin string
	GenAlertsBin    string
	Timeout         time.Duration
}

// NewFromEnv builds a Renderer using GEN_DASHBOARD_BIN / GEN_ALERTS_BIN if
// set, otherwise the plain binary names resolved via $PATH.
func NewFromEnv() *Renderer {
	return &Renderer{
		GenDashboardBin: envOr("GEN_DASHBOARD_BIN", "gen-dashboard"),
		GenAlertsBin:    envOr("GEN_ALERTS_BIN", "gen-alerts"),
		Timeout:         defaultTimeout,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// RenderDashboard runs gen-dashboard and returns the rendered dashboard
// artifact JSON bytes (apiVersion/folder/tags/metadata/dashboard wrapper --
// see obs-golden-signal-templates/README.md).
func (r *Renderer) RenderDashboard(ctx context.Context, manifestYAML []byte) ([]byte, error) {
	return r.run(ctx, r.GenDashboardBin, manifestYAML, "dashboard.json", nil)
}

// RenderAlerts runs gen-alerts and returns the rendered PrometheusRule YAML
// bytes. extraRulesYAML, if non-nil, is written to a sibling file and passed
// via --extra-rules.
func (r *Renderer) RenderAlerts(ctx context.Context, manifestYAML []byte, extraRulesYAML []byte) ([]byte, error) {
	return r.run(ctx, r.GenAlertsBin, manifestYAML, "alerts.yaml", extraRulesYAML)
}

func (r *Renderer) run(ctx context.Context, bin string, manifestYAML []byte, outName string, extraRulesYAML []byte) ([]byte, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "obs-render-*")
	if err != nil {
		return nil, fmt.Errorf("create render tempdir: %w", err)
	}
	defer os.RemoveAll(dir)

	manifestPath := filepath.Join(dir, "observability.yaml")
	if err := os.WriteFile(manifestPath, manifestYAML, 0o600); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	outputPath := filepath.Join(dir, outName)

	args := []string{"--manifest", manifestPath, "--output", outputPath}
	if len(extraRulesYAML) > 0 {
		extraPath := filepath.Join(dir, "extra-rules.yaml")
		if err := os.WriteFile(extraPath, extraRulesYAML, 0o600); err != nil {
			return nil, fmt.Errorf("write extra-rules: %w", err)
		}
		args = append(args, "--extra-rules", extraPath)
	}

	cmd := exec.CommandContext(runCtx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %v: %w: %s", bin, args, err, stderr.String())
	}

	out, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read %s output: %w", bin, err)
	}
	return out, nil
}
