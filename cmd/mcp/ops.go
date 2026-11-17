package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/thanhhoa35142/be-example/internal/modules/admin"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
)

const (
	opsMaxSQLLen   = 8192
	dbQueryMaxRows = 1000
	dbQueryDefRows = 200
)

var selectPrefixRe = regexp.MustCompile(`(?i)^\s*(select|with)\b`)

type jobStatusArgs struct {
	Job    string `json:"job,omitempty" jsonschema:"optional job name filter (e.g. weather.refresh, scoring.recompute)"`
	Status string `json:"status,omitempty" jsonschema:"optional run status filter: running, succeeded, failed, abandoned"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max runs to return (default 20, max 200)"`
}

type jobStatusOut struct {
	Runs      []admin.JobRun      `json:"runs"`
	Schedules []admin.JobSchedule `json:"schedules"`
}

type freshnessOut struct {
	Freshness admin.Freshness `json:"freshness"`
	Overview  admin.Overview  `json:"overview"`
}

type packHealthArgs struct {
	Slug string `json:"slug,omitempty" jsonschema:"optional pack slug; when set returns the full pack detail instead of the list"`
}

// uuid.UUID marshals as a string but the SDK's schema inference treats it
// as a byte array — map admin types to string-ID DTOs for tool output.
type mcpCityPack struct {
	ID           string     `json:"id"`
	Slug         string     `json:"slug"`
	Name         string     `json:"name"`
	Country      string     `json:"country"`
	TZ           string     `json:"tz"`
	Status       string     `json:"status"`
	LastImportAt *time.Time `json:"lastImportAt"`
	ActiveSpots  int64      `json:"activeSpots"`
	TotalSpots   int64      `json:"totalSpots"`
	NextStatuses []string   `json:"nextStatuses"`
}

type mcpCitySpot struct {
	ID        string    `json:"id"`
	Name      *string   `json:"name"`
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type mcpPackDetail struct {
	mcpCityPack
	OsmExtract       *string           `json:"osmExtract"`
	ExtractFetchedAt *time.Time        `json:"extractFetchedAt"`
	PendingReports   int64             `json:"pendingReports"`
	SpotCounts       []admin.NameCount `json:"spotCounts"`
	IngestRoles      []admin.NameCount `json:"ingestRoles"`
	ExcludeReasons   []admin.NameCount `json:"excludeReasons"`
	RecentRuns       []admin.JobRun    `json:"recentRuns"`
	RecentSpots      []mcpCitySpot     `json:"recentSpots"`
}

type packHealthOut struct {
	Packs  []mcpCityPack  `json:"packs,omitempty"`
	Detail *mcpPackDetail `json:"detail,omitempty"`
}

func toMcpPack(p admin.CityPack) mcpCityPack {
	return mcpCityPack{
		ID: p.ID.String(), Slug: p.Slug, Name: p.Name, Country: p.Country,
		TZ: p.Tz, Status: p.Status, LastImportAt: p.LastImportAt,
		ActiveSpots: p.ActiveSpots, TotalSpots: p.TotalSpots,
		NextStatuses: p.NextStatuses,
	}
}

func toMcpPackDetail(d admin.CityPackDetail) *mcpPackDetail {
	spots := make([]mcpCitySpot, 0, len(d.RecentSpots))
	for _, s := range d.RecentSpots {
		spots = append(spots, mcpCitySpot{
			ID: s.ID.String(), Name: s.Name, Kind: s.Kind,
			Status: s.Status, UpdatedAt: s.UpdatedAt,
		})
	}
	return &mcpPackDetail{
		mcpCityPack:      toMcpPack(d.CityPack),
		OsmExtract:       d.OsmExtract,
		ExtractFetchedAt: d.ExtractFetchedAt,
		PendingReports:   d.PendingReports,
		SpotCounts:       d.SpotCounts,
		IngestRoles:      d.IngestRoles,
		ExcludeReasons:   d.ExcludeReasons,
		RecentRuns:       d.RecentRuns,
		RecentSpots:      spots,
	}
}

type dbQueryArgs struct {
	SQL     string `json:"sql" jsonschema:"a single SELECT (or WITH ... SELECT) statement against the read-only allowlisted tables"`
	MaxRows int    `json:"maxRows,omitempty" jsonschema:"max rows to return (default 200, max 1000)"`
}

type dbQueryOut struct {
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"rowCount"`
	Truncated bool             `json:"truncated"`
}

func opsToolError(ctx context.Context, tool string, err error) error {
	switch {
	case errors.Is(err, admin.ErrJobScheduleNotFound), errors.Is(err, admin.ErrJobRunNotFound):
		return errors.New("not found")
	case errors.Is(err, admin.ErrCityPackNotFound):
		return errors.New("city pack not found")
	case errors.Is(err, admin.ErrInvalidFilter):
		return errors.New("invalid filter parameters")
	}
	return toolError(ctx, tool, err)
}

func registerOpsTools(s *mcp.Server, adminSvc *admin.Service, keysSvc *apikeys.Service) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "job_status",
		Description: "Ops visibility for background workers: recent t_job_runs (filter by job name and/or status) plus the configured job schedules (interval, enabled, pending run request). Use this to check whether ingest/weather/scoring jobs ran, failed, or are overdue.",
		Annotations: readOnlyAnnotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args jobStatusArgs) (*mcp.CallToolResult, jobStatusOut, error) {
		if err := requireScope(ctx, keysSvc, "ops", "read"); err != nil {
			return nil, jobStatusOut{}, err
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		var runs []admin.JobRun
		var err error
		if args.Job != "" {
			runs, err = adminSvc.JobRunsForJob(ctx, args.Job, limit)
			if args.Status != "" {
				filtered := runs[:0]
				for _, r := range runs {
					if r.Status == args.Status {
						filtered = append(filtered, r)
					}
				}
				runs = filtered
			}
		} else {
			var page admin.Page[admin.JobRun]
			page, err = adminSvc.Jobs(ctx, limit, nil, args.Status)
			runs = page.Items
		}
		if err != nil {
			return nil, jobStatusOut{}, opsToolError(ctx, "job_status", err)
		}
		schedules, err := adminSvc.JobSchedules(ctx)
		if err != nil {
			return nil, jobStatusOut{}, opsToolError(ctx, "job_status", err)
		}
		return nil, jobStatusOut{Runs: runs, Schedules: schedules}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "data_freshness",
		Description: "Data-pipeline freshness and headline counters: last weather fetch, last score computation, last crowd update (each also counts the last successful job run), plus overview counts (active spots, spots with fresh score, devices, pending reports, alert deliveries). Use to verify ingest/recompute actually produced fresh data.",
		Annotations: readOnlyAnnotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, freshnessOut, error) {
		if err := requireScope(ctx, keysSvc, "ops", "read"); err != nil {
			return nil, freshnessOut{}, err
		}
		fresh, err := adminSvc.Freshness(ctx)
		if err != nil {
			return nil, freshnessOut{}, opsToolError(ctx, "data_freshness", err)
		}
		overview, err := adminSvc.Overview(ctx)
		if err != nil {
			return nil, freshnessOut{}, opsToolError(ctx, "data_freshness", err)
		}
		return nil, freshnessOut{Freshness: fresh, Overview: overview}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "pack_health",
		Description: "Per-city-pack operational health: status, active/total spot counts, last import time. Pass 'slug' for the full detail of one pack (pending reports, spot status breakdown, ingest feature roles, recent job runs).",
		Annotations: readOnlyAnnotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args packHealthArgs) (*mcp.CallToolResult, packHealthOut, error) {
		if err := requireScope(ctx, keysSvc, "ops", "read"); err != nil {
			return nil, packHealthOut{}, err
		}
		packs, err := adminSvc.CityPacks(ctx)
		if err != nil {
			return nil, packHealthOut{}, opsToolError(ctx, "pack_health", err)
		}
		if args.Slug == "" {
			out := make([]mcpCityPack, 0, len(packs))
			for _, p := range packs {
				out = append(out, toMcpPack(p))
			}
			return nil, packHealthOut{Packs: out}, nil
		}
		for _, p := range packs {
			if p.Slug == args.Slug {
				d, err := adminSvc.CityPackDetail(ctx, p.ID)
				if err != nil {
					return nil, packHealthOut{}, opsToolError(ctx, "pack_health", err)
				}
				return nil, packHealthOut{Detail: toMcpPackDetail(d)}, nil
			}
		}
		return nil, packHealthOut{}, errors.New("city pack not found")
	})
}

func registerDBQueryTool(s *mcp.Server, keysSvc *apikeys.Service, pool *pgxpool.Pool) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "db_query",
		Description: "Run a single read-only SELECT against the database through a restricted role: only allowlisted tables are visible (spots, scores, weather, crowd, packs, job runs, schedules, quotas, holidays, reports) and writes are impossible. Use for ad-hoc verification the dedicated tools don't cover. Results are capped at maxRows.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   true,
			IdempotentHint: false,
			OpenWorldHint:  new(bool),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args dbQueryArgs) (*mcp.CallToolResult, dbQueryOut, error) {
		if err := requireScope(ctx, keysSvc, "db", "read"); err != nil {
			return nil, dbQueryOut{}, err
		}
		sql := strings.TrimSpace(args.SQL)
		if sql == "" || len(sql) > opsMaxSQLLen {
			return nil, dbQueryOut{}, errors.New("sql must be 1-8192 chars")
		}
		if !selectPrefixRe.MatchString(sql) {
			return nil, dbQueryOut{}, errors.New("only SELECT (or WITH ... SELECT) statements are allowed")
		}
		maxRows := args.MaxRows
		if maxRows <= 0 {
			maxRows = dbQueryDefRows
		}
		if maxRows > dbQueryMaxRows {
			maxRows = dbQueryMaxRows
		}

		rows, err := pool.Query(ctx, sql)
		if err != nil {
			return nil, dbQueryOut{}, fmt.Errorf("query failed: %w", err)
		}
		defer rows.Close()

		fields := rows.FieldDescriptions()
		columns := make([]string, 0, len(fields))
		seen := map[string]int{}
		for _, f := range fields {
			base := string(f.Name)
			seen[base]++
			name := base
			if seen[base] > 1 {
				name = fmt.Sprintf("%s_%d", base, seen[base])
			}
			columns = append(columns, name)
		}

		out := dbQueryOut{Columns: columns, Rows: []map[string]any{}}
		for rows.Next() {
			if len(out.Rows) >= maxRows {
				out.Truncated = true
				break
			}
			values, err := rows.Values()
			if err != nil {
				return nil, dbQueryOut{}, fmt.Errorf("row decode: %w", err)
			}
			rec := make(map[string]any, len(columns))
			for i, v := range values {
				rec[columns[i]] = normalizeDBValue(v)
			}
			out.Rows = append(out.Rows, rec)
		}
		if err := rows.Err(); err != nil {
			return nil, dbQueryOut{}, fmt.Errorf("query failed: %w", err)
		}
		out.RowCount = len(out.Rows)
		return nil, out, nil
	})
}

func normalizeDBValue(v any) any {
	if b, ok := v.([]byte); ok {
		if utf8.Valid(b) {
			return string(b)
		}
		return b
	}
	return v
}
