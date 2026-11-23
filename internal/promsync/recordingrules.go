package promsync

import (
	"fmt"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
)

// BuildSLORecordingRuleGroup produces the two SLO recording rules
// obs-developer-portal's Health Overview page depends on for its bulk
// (all-services-at-once) query -- see that repo's
// docs/recording-rules-contract.md, which this function is the actual
// implementation of. Without this group, that contract is documentation
// with nothing behind it: the portal's Summary() query
// (obs:slo_error_budget_remaining:percent, obs:slo_burn_rate:ratio1h)
// would simply return no data for every service.
//
// Labels are `service`/`environment` (not `service_id`, which
// recording-rules-contract.md originally specified but nothing else in
// the platform produces -- obs-alert-rule-templates' own alert rules use
// service/environment, and the portal's own key-derivation fallback
// already composes "{service}-{environment}" when service_id is absent,
// so aligning on service/environment everywhere is the change with the
// smaller blast radius).
func BuildSLORecordingRuleGroup(svcObs *obsv1.ServiceObservability) monitoringv1.RuleGroup {
	name := svcObs.Name
	environment := svcObs.Spec.Environment
	team := svcObs.Spec.Ownership.Team
	serviceType := string(svcObs.Spec.ServiceType)
	trafficMetric := svcObs.Spec.GoldenSignals.TrafficMetric
	errorMetric := svcObs.Spec.GoldenSignals.ErrorMetric
	budget := 1 - svcObs.Spec.SLO.AvailabilityTarget

	labels := map[string]string{
		"service":      name,
		"environment":  environment,
		"team":         team,
		"service_type": serviceType,
	}

	errorRatio := func(window string) string {
		return fmt.Sprintf(
			`sum(rate(%s{service=%q,environment=%q}[%s])) / clamp_min(sum(rate(%s{service=%q,environment=%q}[%s])), 1e-9)`,
			errorMetric, name, environment, window, trafficMetric, name, environment, window,
		)
	}
	errorRatioIncrease := func(window string) string {
		return fmt.Sprintf(
			`sum(increase(%s{service=%q,environment=%q}[%s])) / clamp_min(sum(increase(%s{service=%q,environment=%q}[%s])), 1)`,
			errorMetric, name, environment, window, trafficMetric, name, environment, window,
		)
	}

	budgetRemainingExpr := fmt.Sprintf(
		"clamp_min(clamp_max(100 * (1 - (%s) / %g), 100), 0)",
		errorRatioIncrease("30d"), budget,
	)
	burnRate1hExpr := fmt.Sprintf("(%s) / %g", errorRatio("1h"), budget)

	return monitoringv1.RuleGroup{
		Name: name + ".slo.recording",
		Rules: []monitoringv1.Rule{
			{
				Record: "obs:slo_error_budget_remaining:percent",
				Expr:   intstr.FromString(budgetRemainingExpr),
				Labels: labels,
			},
			{
				Record: "obs:slo_burn_rate:ratio1h",
				Expr:   intstr.FromString(burnRate1hExpr),
				Labels: labels,
			},
		},
	}
}
