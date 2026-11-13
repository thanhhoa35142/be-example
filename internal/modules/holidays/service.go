package holidays

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Row is one holiday to upsert (national, kind 'public').
type Row struct {
	Country string
	Date    time.Time
	Name    string
}

// Repository is the port; implemented in infrastructure/persistence.
type Repository interface {
	// Countries returns the distinct countries of the (non-retired) city packs.
	Countries(ctx context.Context) ([]string, error)
	// Upsert writes national public holidays idempotently and returns rows affected.
	Upsert(ctx context.Context, rows []Row) (int64, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Stats is persisted into t_job_runs.stats.
type Stats struct {
	Countries  int            `json:"countries"`
	Years      []int          `json:"years"`
	Rows       int64          `json:"rows_upserted"`
	Uncovered  []string       `json:"uncovered_countries"`
	PerCountry map[string]int `json:"per_country"`
}

// Refresh upserts the current and next year for every pack country.
func (s *Service) Refresh(ctx context.Context, now time.Time) (Stats, error) {
	countries, err := s.repo.Countries(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("countries: %w", err)
	}
	years := []int{now.Year(), now.Year() + 1}
	st := Stats{Years: years, Uncovered: []string{}, PerCountry: map[string]int{}}

	var rows []Row
	for _, c := range countries {
		if !Covered(c) {
			st.Uncovered = append(st.Uncovered, c)
			slog.Warn("holidays: country not covered, skipped", "country", c)
			continue
		}
		n := 0
		for _, y := range years {
			days, _ := For(c, y)
			for _, d := range days {
				rows = append(rows, Row{Country: c, Date: d.Date, Name: d.Name})
				n++
			}
		}
		st.Countries++
		st.PerCountry[c] = n
	}
	if len(rows) == 0 {
		return st, nil
	}
	st.Rows, err = s.repo.Upsert(ctx, rows)
	if err != nil {
		return st, fmt.Errorf("upsert: %w", err)
	}
	return st, nil
}
