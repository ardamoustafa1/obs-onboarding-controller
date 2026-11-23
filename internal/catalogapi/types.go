// Package catalogapi is the wire format shared between the catalog server
// (cmd/catalog, internal/catalog) and the operator's catalog client
// (internal/catalogclient). Kept separate from both so neither has to
// import the other.
package catalogapi

import "time"

const (
	StatusActive         = "active"
	StatusDecommissioned = "decommissioned"
)

// ServiceRecord is one row of the service catalog: what a portal reads to
// answer "what services exist, who owns them, where's their dashboard".
//
// The SLO/golden-signal fields (AvailabilityTarget onward) exist because
// obs-developer-portal needs them to compute error budgets and build
// PromQL without guessing metric names from serviceType alone -- see that
// repo's docs/recording-rules-contract.md, which this record's fields are
// the source of truth for (populated by the operator's syncCatalog, from
// the same ServiceObservability CR spec that feeds gen-alerts).
type ServiceRecord struct {
	Name             string    `json:"name"`
	Environment      string    `json:"environment"`
	Team             string    `json:"team"`
	ServiceType      string    `json:"serviceType"`
	DashboardURL     string    `json:"dashboardUrl,omitempty"`
	SlackChannel     string    `json:"slackChannel,omitempty"`
	PagerDutyService string    `json:"pagerDutyService,omitempty"`
	Status           string    `json:"status"`
	UpdatedAt        time.Time `json:"updatedAt"`

	AvailabilityTarget float64 `json:"availabilityTarget,omitempty"`
	LatencyTargetMS    int32   `json:"latencyTargetMs,omitempty"`
	LatencyPercentile  int32   `json:"latencyPercentile,omitempty"`
	TrafficMetric      string  `json:"trafficMetric,omitempty"`
	ErrorMetric        string  `json:"errorMetric,omitempty"`
	// LatencyMetric is whichever of the manifest's latencyMetric /
	// processingDurationMetric applies to this service's serviceType.
	LatencyMetric string `json:"latencyMetric,omitempty"`
}

// Key uniquely identifies a record: the same service name can exist
// independently in dev/staging/prod.
func (r ServiceRecord) Key() string {
	return r.Environment + "/" + r.Name
}
