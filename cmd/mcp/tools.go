package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/modules/recommendations"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
)

// --- Output DTOs (flat camelCase JSON for agent consumption) ---

type scoreOut struct {
	Score      *int           `json:"score,omitempty"`
	Confidence *int           `json:"confidence,omitempty"`
	CapReason  *string        `json:"capReason,omitempty"`
	Reasons    []spots.Reason `json:"reasons,omitempty"`
	ComputedAt *time.Time     `json:"computedAt,omitempty"`
}

type spotOut struct {
	ID      string      `json:"id"`
	Kind    string      `json:"kind"`
	Name    *string     `json:"name,omitempty"`
	Names   spots.Names `json:"names,omitempty"`
	Lat     float64     `json:"lat"`
	Lon     float64     `json:"lon"`
	City    *string     `json:"city,omitempty"`
	Country string      `json:"country"`
	TZ      string      `json:"tz"`
	Score   *scoreOut   `json:"score,omitempty"`
}

type spotDetailOut struct {
	spotOut
	Amenities []spots.Amenity `json:"amenities,omitempty"`
	Sources   []spots.Source  `json:"sources,omitempty"`
}

type recOut struct {
	spotOut
	DistanceM float64 `json:"distanceM"`
}

func toScoreOut(s *spots.ScoreSnapshot) *scoreOut {
	if s == nil {
		return nil
	}
	return &scoreOut{
		Score:      s.Score,
		Confidence: s.Confidence,
		CapReason:  s.CapReason,
		Reasons:    s.Reasons,
		ComputedAt: s.ComputedAt,
	}
}

func toSpotOut(s spots.Spot, score *spots.ScoreSnapshot) spotOut {
	return spotOut{
		ID:      s.ID.String(),
		Kind:    string(s.Kind),
		Name:    s.Name,
		Names:   s.Names,
		Lat:     s.Lat,
		Lon:     s.Lon,
		City:    s.City,
		Country: s.Country,
		TZ:      s.TZ,
		Score:   toScoreOut(score),
	}
}

// --- Tool input types ---

type bboxArgs struct {
	West  float64 `json:"west" jsonschema:"western longitude (degrees)"`
	South float64 `json:"south" jsonschema:"southern latitude (degrees)"`
	East  float64 `json:"east" jsonschema:"eastern longitude (degrees)"`
	North float64 `json:"north" jsonschema:"northern latitude (degrees)"`
	When  string  `json:"when,omitempty" jsonschema:"optional RFC3339 timestamp; defaults to current hour"`
}

type spotIDArgs struct {
	ID string `json:"id" jsonschema:"spot UUID"`
}

type recommendArgs struct {
	Lat     float64 `json:"lat" jsonschema:"latitude (degrees)"`
	Lon     float64 `json:"lon" jsonschema:"longitude (degrees)"`
	RadiusM float64 `json:"radiusM,omitempty" jsonschema:"search radius in meters (default 5000, max 50000)"`
	Limit   int     `json:"limit,omitempty" jsonschema:"max results (default 3, max 50)"`
	When    string  `json:"when,omitempty" jsonschema:"optional RFC3339 timestamp; defaults to current hour"`
}

func parseWhen(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("invalid 'when' timestamp, want RFC3339: %w", err)
	}
	return &t, nil
}

func parseSpotID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid spot id: %w", err)
	}
	return id, nil
}

// --- Registration ---

func registerTools(s *mcp.Server, spotsSvc *spots.Service, recSvc *recommendations.Service, keysSvc *apikeys.Service) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "spots_in_bbox",
		Description: "List picnic spots with their 0-100 score inside a bounding box. Scores are hourly; pass 'when' (RFC3339) to pick a different hour, default is now.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args bboxArgs) (*mcp.CallToolResult, []spotOut, error) {
		if err := requireScope(ctx, keysSvc, "spots", "read"); err != nil {
			return nil, nil, err
		}
		when, err := parseWhen(args.When)
		if err != nil {
			return nil, nil, err
		}
		items, err := spotsSvc.ListInBBox(ctx, spots.BBox{
			West: args.West, South: args.South, East: args.East, North: args.North,
		}, when)
		if err != nil {
			return nil, nil, err
		}
		out := make([]spotOut, 0, len(items))
		for _, it := range items {
			out = append(out, toSpotOut(it.Spot, it.Score))
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "spot_get",
		Description: "Get one spot's details: location, names, amenities, and data sources. Use spot_forecast for its hourly scores.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args spotIDArgs) (*mcp.CallToolResult, spotDetailOut, error) {
		if err := requireScope(ctx, keysSvc, "spots", "read"); err != nil {
			return nil, spotDetailOut{}, err
		}
		id, err := parseSpotID(args.ID)
		if err != nil {
			return nil, spotDetailOut{}, err
		}
		d, err := spotsSvc.Get(ctx, id)
		if err != nil {
			return nil, spotDetailOut{}, err
		}
		return nil, spotDetailOut{
			spotOut:   toSpotOut(d.Spot, nil),
			Amenities: d.Amenities,
			Sources:   d.Sources,
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "spot_forecast",
		Description: "Get a spot's hourly 0-100 score forecast for the next 7 days, including score components and reason codes.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args spotIDArgs) (*mcp.CallToolResult, []spots.ForecastPoint, error) {
		if err := requireScope(ctx, keysSvc, "spots", "read"); err != nil {
			return nil, nil, err
		}
		id, err := parseSpotID(args.ID)
		if err != nil {
			return nil, nil, err
		}
		pts, err := spotsSvc.Forecast(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		return nil, pts, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "recommend_spots",
		Description: "Rank the best picnic spots around a lat/lon at a given hour. Returns up to 'limit' spots sorted by score with distance.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args recommendArgs) (*mcp.CallToolResult, []recOut, error) {
		if err := requireScope(ctx, keysSvc, "recommendations", "read"); err != nil {
			return nil, nil, err
		}
		when, err := parseWhen(args.When)
		if err != nil {
			return nil, nil, err
		}
		q := recommendations.Query{
			Lat:     args.Lat,
			Lon:     args.Lon,
			RadiusM: args.RadiusM,
			Limit:   args.Limit,
		}
		if when != nil {
			q.When = *when
		}
		items, err := recSvc.Recommend(ctx, q)
		if err != nil {
			return nil, nil, err
		}
		out := make([]recOut, 0, len(items))
		for _, it := range items {
			o := recOut{spotOut: toSpotOut(it.Spot, it.Score), DistanceM: it.DistanceM}
			out = append(out, o)
		}
		return nil, out, nil
	})
}
