// Package manifestbuild renders a ServiceObservability CR into the exact
// YAML shape obs-manifest-schema/obs-golden-signal-templates/obs-alert-rule-templates
// expect on --manifest. The operator never hand-maintains a second copy of
// that schema in Go structs read by those tools directly (they are separate
// repos/binaries, invoked as CLIs -- see internal/gentools) -- it only needs
// to produce bytes those CLIs already know how to parse.
package manifestbuild

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	obsv1 "github.com/acme/obs-onboarding-controller/api/v1"
)

// manifestDoc mirrors obs-manifest-schema v2's on-disk shape.
type manifestDoc struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   metadataDoc `yaml:"metadata"`
	Spec       specDoc     `yaml:"spec"`
}

type metadataDoc struct {
	Name string `yaml:"name"`
}

type specDoc struct {
	ServiceType   string           `yaml:"serviceType"`
	Environment   string           `yaml:"environment"`
	SLO           sloDoc           `yaml:"slo"`
	GoldenSignals goldenSignalsDoc `yaml:"goldenSignals"`
	Dashboards    dashboardsDoc    `yaml:"dashboards"`
	Alerting      alertingDoc      `yaml:"alerting"`
	Ownership     ownershipDoc     `yaml:"ownership"`
}

type sloDoc struct {
	AvailabilityTarget float64 `yaml:"availability_target"`
	LatencyTargetMS    int32   `yaml:"latency_target_ms"`
	LatencyPercentile  int32   `yaml:"latency_percentile"`
}

type goldenSignalsDoc struct {
	TrafficMetric            string   `yaml:"trafficMetric"`
	ErrorMetric              string   `yaml:"errorMetric"`
	LatencyMetric            string   `yaml:"latencyMetric,omitempty"`
	ProcessingDurationMetric string   `yaml:"processingDurationMetric,omitempty"`
	SaturationMetrics        []string `yaml:"saturationMetrics"`
}

type dashboardsDoc struct {
	Template string `yaml:"template"`
}

type alertingDoc struct {
	BurnRateAlerts bool           `yaml:"burnRateAlerts"`
	ExtraRules     []extraRuleDoc `yaml:"extraRules,omitempty"`
}

type extraRuleDoc struct {
	Name     string `yaml:"name"`
	Expr     string `yaml:"expr"`
	For      string `yaml:"for,omitempty"`
	Severity string `yaml:"severity"`
	Summary  string `yaml:"summary,omitempty"`
}

type ownershipDoc struct {
	Team             string `yaml:"team"`
	SlackChannel     string `yaml:"slackChannel"`
	PagerDutyService string `yaml:"pagerDutyService"`
}

// Build renders a ServiceObservability CR (identified by serviceName) into
// obs-manifest-schema v2 YAML bytes.
func Build(serviceName string, spec obsv1.ServiceObservabilitySpec) ([]byte, error) {
	doc := manifestDoc{
		APIVersion: "obs.acme.com/v2",
		Kind:       "ServiceObservability",
		Metadata:   metadataDoc{Name: serviceName},
		Spec: specDoc{
			ServiceType: string(spec.ServiceType),
			Environment: spec.Environment,
			SLO: sloDoc{
				AvailabilityTarget: spec.SLO.AvailabilityTarget,
				LatencyTargetMS:    spec.SLO.LatencyTargetMS,
				LatencyPercentile:  spec.SLO.LatencyPercentile,
			},
			GoldenSignals: goldenSignalsDoc{
				TrafficMetric:            spec.GoldenSignals.TrafficMetric,
				ErrorMetric:              spec.GoldenSignals.ErrorMetric,
				LatencyMetric:            spec.GoldenSignals.LatencyMetric,
				ProcessingDurationMetric: spec.GoldenSignals.ProcessingDurationMetric,
				SaturationMetrics:        spec.GoldenSignals.SaturationMetrics,
			},
			Dashboards: dashboardsDoc{Template: spec.Dashboards.Template},
			Alerting: alertingDoc{
				BurnRateAlerts: spec.Alerting.BurnRateAlerts,
				ExtraRules:     extraRules(spec.Alerting.ExtraRules),
			},
			Ownership: ownershipDoc{
				Team:             spec.Ownership.Team,
				SlackChannel:     spec.Ownership.SlackChannel,
				PagerDutyService: spec.Ownership.PagerDutyService,
			},
		},
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest for %s: %w", serviceName, err)
	}
	return out, nil
}

func extraRules(in []obsv1.ExtraAlertRule) []extraRuleDoc {
	if len(in) == 0 {
		return nil
	}
	out := make([]extraRuleDoc, len(in))
	for i, r := range in {
		out[i] = extraRuleDoc{
			Name:     r.Name,
			Expr:     r.Expr,
			For:      r.For,
			Severity: r.Severity,
			Summary:  r.Summary,
		}
	}
	return out
}
