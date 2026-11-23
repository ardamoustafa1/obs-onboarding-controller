package promsync

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
)

func TestBuildSLORecordingRuleGroup(t *testing.T) {
	svcObs := &obsv1.ServiceObservability{
		ObjectMeta: metav1.ObjectMeta{Name: "checkout-api"},
		Spec: obsv1.ServiceObservabilitySpec{
			ServiceType: obsv1.ServiceTypeHTTP,
			Environment: "prod",
			SLO:         obsv1.SLOSpec{AvailabilityTarget: 0.999},
			GoldenSignals: obsv1.GoldenSignalsSpec{
				TrafficMetric: "http_server_requests_total",
				ErrorMetric:   "http_server_request_errors_total",
			},
			Ownership: obsv1.OwnershipSpec{Team: "team-checkout"},
		},
	}

	group := BuildSLORecordingRuleGroup(svcObs)

	if len(group.Rules) != 2 {
		t.Fatalf("expected 2 recording rules, got %d", len(group.Rules))
	}

	budgetRule := group.Rules[0]
	if budgetRule.Record != "obs:slo_error_budget_remaining:percent" {
		t.Errorf("unexpected record name: %s", budgetRule.Record)
	}
	if budgetRule.Labels["service"] != "checkout-api" || budgetRule.Labels["environment"] != "prod" ||
		budgetRule.Labels["team"] != "team-checkout" || budgetRule.Labels["service_type"] != "http" {
		t.Errorf("unexpected labels: %#v", budgetRule.Labels)
	}
	expr := budgetRule.Expr.String()
	if !strings.Contains(expr, "http_server_request_errors_total") || !strings.Contains(expr, "http_server_requests_total") {
		t.Errorf("expected budget rule expr to reference both golden-signal metrics, got: %s", expr)
	}
	if !strings.Contains(expr, "[30d]") {
		t.Errorf("expected a 30d window per docs/recording-rules-contract.md, got: %s", expr)
	}

	burnRule := group.Rules[1]
	if burnRule.Record != "obs:slo_burn_rate:ratio1h" {
		t.Errorf("unexpected record name: %s", burnRule.Record)
	}
	if !strings.Contains(burnRule.Expr.String(), "[1h]") {
		t.Errorf("expected a 1h window, got: %s", burnRule.Expr.String())
	}
}
