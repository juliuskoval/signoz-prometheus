package server

import "github.com/SigNoz/signoz/pkg/types/metrictypes"

// Types reproduced from github.com/SigNoz/signoz to avoid pulling in the
// entire SigNoz module as a dependency.  Only the fields actually used by
// this proxy are included; unused fields are omitted intentionally.

// --- telemetrytypes ---

type TelemetryFieldKey struct {
	Name string `json:"name"`
}

type TelemetryFieldValues struct {
	StringValues []string `json:"stringValues,omitempty"`
}

// --- v3 ---

type FilterOperator string

const (
	FilterOperatorEqual       FilterOperator = "="
	FilterOperatorNotEqual    FilterOperator = "!="
	FilterOperatorContains    FilterOperator = "contains"
	FilterOperatorNotContains FilterOperator = "ncontains"
)

type AttributeKey struct {
	Key string `json:"key"`
}

type FilterItem struct {
	Key      AttributeKey   `json:"key"`
	Value    any            `json:"value"`
	Operator FilterOperator `json:"op"`
}

type FilterSet struct {
	Operator string       `json:"op,omitempty"`
	Items    []FilterItem `json:"items"`
}

// --- metrics_explorer ---

type MetricDetail struct {
	MetricName string `json:"metric_name"`
}

type SummaryListMetricsRequest struct {
	Offset  int       `json:"offset"`
	Limit   int       `json:"limit"`
	Start   int64     `json:"start"`
	End     int64     `json:"end"`
	Filters FilterSet `json:"filters"`
}

type SummaryListMetricsResponse struct {
	Metrics []MetricDetail `json:"metrics"`
}

type MetricDetailsDTO struct {
	Description string `json:"description"`
	Type        string `json:"type"`
	Unit        string `json:"unit"`
}

// --- metricsexplorertypes ---

type ListMetricsResponse struct {
	Metrics []ListMetric `json:"metrics" required:"true" nullable:"true"`
}

type ListMetric struct {
	MetricName  string                  `json:"metricName" required:"true"`
	Description string                  `json:"description" required:"true"`
	MetricType  metrictypes.Type        `json:"type" required:"true"`
	MetricUnit  string                  `json:"unit" required:"true"`
	Temporality metrictypes.Temporality `json:"temporality" required:"true"`
	IsMonotonic bool                    `json:"isMonotonic" required:"true"`
}
