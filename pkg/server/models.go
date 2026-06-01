package server

// Types reproduced from github.com/SigNoz/signoz to avoid pulling in the
// entire SigNoz module as a dependency.  Only the fields actually used by
// this proxy are included; unused fields are omitted intentionally.

type apiResponse struct {
	Status string `json:"status"`
	Data   any    `json:"data,omitempty"`
}

type description struct {
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}

// --- clickhouse query_range ---

type ClickHouseQuery struct {
	Query    string `json:"query"`
	Disabled bool   `json:"disabled"`
}

type CompositeQuery struct {
	QueryType string                     `json:"queryType"`
	PanelType string                     `json:"panelType"`
	ChQueries map[string]ClickHouseQuery `json:"chQueries"`
}

type QueryRangeRequest struct {
	Start          int64          `json:"start"`
	End            int64          `json:"end"`
	Step           int64          `json:"step"`
	CompositeQuery CompositeQuery `json:"compositeQuery"`
}

// QueryRangeResponse models the `data` object of a query_range response
// (github.com/SigNoz/signoz .../model/v3.QueryRangeResponse). A clickhouse_sql
// query that is not formatted for web (FormatForWeb=false) comes back as time
// series under `series`, where each string column becomes a label; only when
// the panel is formatted for web are rows returned under `list`/`table.rows`.
// We read all three so the same query works regardless.
type QueryRangeResponse struct {
	Result []struct {
		QueryName string         `json:"queryName"`
		Series    []SeriesLabels `json:"series"`
		List      []QueryRow     `json:"list"`
		Table     *struct {
			Rows []QueryRow `json:"rows"`
		} `json:"table"`
	} `json:"result"`
}

type SeriesLabels struct {
	Labels map[string]string `json:"labels"`
}

type QueryRow struct {
	Data map[string]any `json:"data"`
}

// --- metrics_explorer ---

type MetricDetailsDTO struct {
	Description string `json:"description"`
	Type        string `json:"type"`
	Unit        string `json:"unit"`
}
