package querybuilder

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	"go.uber.org/zap"
)

// PrometheusMetricName is the reserved label whose values are metric names.
const PrometheusMetricName = "__name__"

const (
	clickhouseMetricName = "metric_name"
	matchQueryParamName  = "match[]"
	timeSeriesTable      = "signoz_metrics.distributed_time_series_v4"
)

// BuildGetLabelsQuery builds a query for the distinct label names present on
// metric time series.
func BuildGetLabelsQuery(q url.Values) (string, error) {
	conds, err := baseConditions(q)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("SELECT DISTINCT arrayJoin(JSONExtractKeys(labels)) AS label_name "+
		"FROM %s %s ORDER BY label_name ASC LIMIT %s",
		timeSeriesTable, whereClause(conds), parseLimit(q)), nil
}

// BuildGetLabelValuesQuery builds a query for the distinct values of a single
// label.
func BuildGetLabelValuesQuery(label string, q url.Values) (string, error) {
	key := escapeCHString(label)

	conds, err := baseConditions(q)
	if err != nil {
		return "", err
	}

	// Exclude rows where the label is absent: JSONExtractString returns "" for
	// a missing key, which would otherwise surface as a spurious empty value.
	conds = append(conds, fmt.Sprintf("JSONExtractString(labels, '%s') != ''", key))

	return fmt.Sprintf("SELECT DISTINCT JSONExtractString(labels, '%s') AS label_value "+
		"FROM %s %s ORDER BY label_value ASC LIMIT %s",
		key, timeSeriesTable, whereClause(conds), parseLimit(q)), nil
}

// BuildGetSeriesQuery builds a query for the distinct metric names (the values
// of __name__), applying any metric_name / label conditions from match[].
func BuildGetSeriesQuery(q url.Values) (string, error) {
	conds, err := baseConditions(q)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("SELECT DISTINCT metric_name "+
		"FROM %s %s ORDER BY metric_name ASC LIMIT %s",
		timeSeriesTable, whereClause(conds), parseLimit(q)), nil
}

// baseConditions returns the WHERE conditions shared by every metrics query:
// the __normalized filter, the match[] selector, and the time bounds. SigNoz
// stores series under two naming schemes — normalized (Prometheus-style, with
// underscores) and un-normalized (original OTel dotted names); __normalized =
// false selects the dotted form and dedupes the pair.
func baseConditions(q url.Values) ([]string, error) {
	mconds, err := matchConditions(q)
	if err != nil {
		return nil, err
	}

	conds := []string{"__normalized = false"}
	conds = append(conds, mconds...)
	conds = append(conds, timeConditions(q)...)
	return conds, nil
}

// matchConditions translates the match[] selector into ClickHouse WHERE
// conditions. It returns nil when match[] is absent; an unparseable selector is
// a client error.
func matchConditions(q url.Values) ([]string, error) {
	match := strings.ReplaceAll(q.Get(matchQueryParamName), "\"\"", "\"")
	if match == "" {
		return nil, nil
	}

	matcher, err := parser.ParseMetricSelector(match)
	if err != nil {
		return nil, fmt.Errorf("invalid match[] selector %q: %w", match, err)
	}

	conds := make([]string, 0, len(matcher))
	for _, m := range matcher {
		conds = append(conds, getCondition(m))
	}
	return conds, nil
}

// millisPerHour is one hour in milliseconds. time_series_v4 buckets rows on
// hour boundaries (unix_milli), so the lower time bound is floored to it.
const millisPerHour int64 = 60 * 60 * 1000

// timeConditions returns unix_milli bounds for whichever of start/end (both in
// seconds) are present and parseable. The lower bound is rounded down to the
// nearest hour so the hour bucket containing start is not excluded.
func timeConditions(q url.Values) []string {
	var conds []string
	if start, err := strconv.ParseInt(q.Get("start"), 10, 64); err == nil {
		startMs := start * 1000
		startMs -= startMs % millisPerHour
		conds = append(conds, fmt.Sprintf("unix_milli >= %d", startMs))
	}
	if end, err := strconv.ParseInt(q.Get("end"), 10, 64); err == nil {
		conds = append(conds, fmt.Sprintf("unix_milli <= %d", end*1000))
	}
	return conds
}

// whereClause joins conditions into a WHERE clause, or "" when there are none.
func whereClause(conds []string) string {
	if len(conds) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(conds, " AND ")
}

const defaultLimit = 1000

// parseLimit returns the validated, positive limit from the query params,
// falling back to defaultLimit when it is absent, non-numeric, or <= 0. The
// parsed value is re-emitted (not the raw string) so only digits reach the SQL.
func parseLimit(q url.Values) string {
	n := int64(defaultLimit)
	if limitStr := q.Get("limit"); limitStr != "" {
		if v, err := strconv.ParseInt(limitStr, 10, 64); err == nil && v > 0 {
			n = v
		} else {
			zap.L().Warn("Invalid limit parameter, using default", zap.String("limit", limitStr))
		}
	}
	return strconv.FormatInt(n, 10)
}

func getCondition(m *labels.Matcher) string {
	name := getName(m)

	switch m.Type {
	case labels.MatchEqual:
		return fmt.Sprintf("%s = '%s'", name, escapeCHString(m.Value))
	case labels.MatchNotEqual:
		return fmt.Sprintf("%s != '%s'", name, escapeCHString(m.Value))
	case labels.MatchRegexp:
		return fmt.Sprintf("%s ILIKE '%s'", name, regexpToILIKE(m.Value))
	default:
		return fmt.Sprintf("%s NOT ILIKE '%s'", name, regexpToILIKE(m.Value))
	}
}

// regexpToILIKE converts a Prometheus regexp matcher value into a case-insensitive
// ILIKE pattern by translating a leading and/or trailing ".*" into the "%" wildcard.
// The remaining text is treated as a literal substring.
func regexpToILIKE(value string) string {
	prefix, suffix := "", ""
	if strings.HasPrefix(value, ".*") {
		value = value[2:]
		prefix = "%"
	}
	if strings.HasSuffix(value, ".*") {
		value = value[:len(value)-2]
		suffix = "%"
	}
	return prefix + escapeCHString(value) + suffix
}

func getName(m *labels.Matcher) string {
	if m.Name == PrometheusMetricName {
		return clickhouseMetricName
	}

	return fmt.Sprintf("JSONExtractString(labels, '%s')", escapeCHString(m.Name))
}

// escapeCHString escapes a string for safe inclusion in a single-quoted
// ClickHouse string literal.
func escapeCHString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}
