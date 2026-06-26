package querybuilder

import (
	"net/url"
	"strings"
	"testing"
)

// vals builds url.Values from "key","value" pairs.
func vals(pairs ...string) url.Values {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		q.Set(pairs[i], pairs[i+1])
	}
	return q
}

func TestBuildGetLabelsQuery(t *testing.T) {
	got, err := BuildGetLabelsQuery(vals("start", "1780112640", "end", "1780134300"))
	if err != nil {
		t.Fatal(err)
	}
	// start 1780112640 (12:44:00Z) is floored to the hour -> 1780110000 (12:00:00Z).
	want := "SELECT DISTINCT arrayJoin(JSONExtractKeys(labels)) AS label_name " +
		"FROM signoz_metrics.distributed_time_series_v4 " +
		"WHERE __normalized = false AND unix_milli >= 1780110000000 AND unix_milli <= 1780134300000 " +
		"ORDER BY label_name ASC LIMIT 1000"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildGetLabelValuesQuery(t *testing.T) {
	// Dotted (OTel) label names must end up quoted, and the absent-key guard present.
	got, err := BuildGetLabelValuesQuery("service.name", vals("limit", "50"))
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT DISTINCT JSONExtractString(labels, 'service.name') AS label_value " +
		"FROM signoz_metrics.distributed_time_series_v4 " +
		"WHERE __normalized = false AND JSONExtractString(labels, 'service.name') != '' " +
		"ORDER BY label_value ASC LIMIT 50"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBuildGetSeriesQueryWithMatch(t *testing.T) {
	got, err := BuildGetSeriesQuery(vals("match[]", `{__name__=~".*demo.*"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT DISTINCT metric_name " +
		"FROM signoz_metrics.distributed_time_series_v4 " +
		"WHERE __normalized = false AND metric_name ILIKE '%demo%' " +
		"ORDER BY metric_name ASC LIMIT 1000"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAllBuildersFilterNormalized(t *testing.T) {
	q := vals()
	labels, _ := BuildGetLabelsQuery(q)
	values, _ := BuildGetLabelValuesQuery("k", q)
	series, _ := BuildGetSeriesQuery(q)
	for name, sql := range map[string]string{"labels": labels, "values": values, "series": series} {
		if !strings.Contains(sql, "__normalized = false") {
			t.Errorf("%s query missing __normalized filter: %s", name, sql)
		}
	}
}

func TestBuildersRejectBadSelector(t *testing.T) {
	// Unquoted dotted label name is invalid PromQL -> client error from every builder.
	q := vals("match[]", `{service.name="x"}`)
	if _, err := BuildGetLabelsQuery(q); err == nil {
		t.Error("BuildGetLabelsQuery: want error, got nil")
	}
	if _, err := BuildGetSeriesQuery(q); err == nil {
		t.Error("BuildGetSeriesQuery: want error, got nil")
	}
	if _, err := BuildGetLabelValuesQuery("k", q); err == nil {
		t.Error("BuildGetLabelValuesQuery: want error, got nil")
	}
}

func TestStartRoundsDownToHour(t *testing.T) {
	// 1780112640 = 12:44:00Z -> floored to 12:00:00Z = 1780110000 (x1000 ms).
	got, err := BuildGetSeriesQuery(vals("start", "1780112640", "end", "1780134300"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "unix_milli >= 1780110000000") {
		t.Errorf("lower bound not floored to the hour: %s", got)
	}
	// The upper bound is left untouched.
	if !strings.Contains(got, "unix_milli <= 1780134300000") {
		t.Errorf("upper bound should not be rounded: %s", got)
	}
}

func TestStartAlreadyOnHourBoundary(t *testing.T) {
	// 1780110000 = 12:00:00Z, already aligned -> unchanged.
	got, _ := BuildGetSeriesQuery(vals("start", "1780110000"))
	if !strings.Contains(got, "unix_milli >= 1780110000000") {
		t.Errorf("aligned start should be unchanged: %s", got)
	}
}

func TestEmptyMatchIsValid(t *testing.T) {
	if _, err := BuildGetLabelsQuery(url.Values{}); err != nil {
		t.Errorf("empty match[] should be valid (means all), got %v", err)
	}
}

func TestEscapeCHString(t *testing.T) {
	cases := map[string]string{
		`abc`:     `abc`,
		`o'brien`: `o\'brien`,
		`a\b`:     `a\\b`,
		`'; DROP`: `\'; DROP`,
	}
	for in, want := range cases {
		if got := escapeCHString(in); got != want {
			t.Errorf("escapeCHString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapingPreventsInjection(t *testing.T) {
	// A value attempting to break out of the string literal must be escaped.
	got, err := BuildGetSeriesQuery(vals("match[]", `{__name__="x') OR 1=1 --"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `metric_name = 'x\') OR 1=1 --'`) {
		t.Errorf("value not properly escaped: %s", got)
	}
}

func TestParseLimit(t *testing.T) {
	cases := map[string]string{
		"":      "1000", // absent -> default
		"40000": "40000",
		"+5":    "5",    // normalized
		"-5":    "1000", // non-positive -> default
		"0":     "1000", // zero -> default
		"abc":   "1000", // non-numeric -> default
	}
	for in, want := range cases {
		q := url.Values{}
		if in != "" {
			q.Set("limit", in)
		}
		if got := parseLimit(q); got != want {
			t.Errorf("parseLimit(%q) = %q, want %q", in, got, want)
		}
	}
}
