package manifestbuild_test

import (
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
	"github.com/acme/obs-onboarding-controller/internal/manifestbuild"
)

func TestBuildRoundTripsThroughYAML(t *testing.T) {
	spec := obsv1.ServiceObservabilitySpec{
		ServiceType: obsv1.ServiceTypeHTTP,
		Environment: "prod",
		SLO:         obsv1.SLOSpec{AvailabilityTarget: 0.999, LatencyTargetMS: 300, LatencyPercentile: 99},
		GoldenSignals: obsv1.GoldenSignalsSpec{
			TrafficMetric:     "http_server_requests_total",
			ErrorMetric:       "http_server_request_errors_total",
			LatencyMetric:     "http_server_request_duration_seconds",
			SaturationMetrics: []string{"cpu_utilization_ratio"},
		},
		Dashboards: obsv1.DashboardsSpec{Template: "http-service-v2"},
		Alerting: obsv1.AlertingSpec{
			BurnRateAlerts: true,
			ExtraRules: []obsv1.ExtraAlertRule{
				{Name: "BacklogHigh", Expr: "queue_depth > 100", Severity: "warning"},
			},
		},
		Ownership: obsv1.OwnershipSpec{Team: "team-checkout", SlackChannel: "#checkout-alerts", PagerDutyService: "checkout-production"},
	}

	out, err := manifestbuild.Build("checkout-api", spec)
	if err != nil {
		t.Fatal(err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v", err)
	}
	if doc["apiVersion"] != "obs.acme.com/v2" {
		t.Errorf("expected apiVersion obs.acme.com/v2, got %v", doc["apiVersion"])
	}
	if !strings.Contains(string(out), "name: checkout-api") {
		t.Errorf("expected metadata.name in output, got:\n%s", out)
	}
	if !strings.Contains(string(out), "BacklogHigh") {
		t.Errorf("expected extraRules entry in output, got:\n%s", out)
	}
}
