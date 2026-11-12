package alerts_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/alerts"
	"github.com/thanhhoa35142/be-example/internal/modules/entitlement"
	"github.com/thanhhoa35142/be-example/internal/modules/notifier"
	"log/slog"
)

func TestEvaluateSmoke(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := persistence.New(pool)
	alertsRepo := persistence.NewAlertsRepo(q)
	svc := alerts.NewService(
		alertsRepo,
		entitlement.NewService(persistence.NewEntitlementRepo(q)),
		notifier.NewSender(slog.Default(), alertsRepo, nil),
	)
	start := time.Now()
	st, err := svc.Evaluate(ctx, time.Now())
	t.Logf("elapsed=%s stats=%+v err=%v", time.Since(start), st, err)
	if err != nil {
		t.Fatal(err)
	}
}
