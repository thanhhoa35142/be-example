// Command keyctl is the staff CLI for managing scoped API keys used by
// MCP/agent clients. The raw secret is printed exactly once on create.
//
// Usage:
//
//	keyctl create --name "ops-agent" --grant spots:read --grant recommendations:read [--expires 720h] [--desc "..."]
//	keyctl list
//	keyctl revoke <key-id>
//	keyctl grant add <key-id> spots:read
//	keyctl grant rm <key-id> spots:read
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/apikeys"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// parseGrant converts "resource:action" ("*" allowed) into a Grant.
// A bare "resource" is treated as resource:read? No — require explicit action.
func parseGrant(spec string) (apikeys.Grant, error) {
	resource, action, ok := strings.Cut(spec, ":")
	if !ok || resource == "" || action == "" {
		return apikeys.Grant{}, fmt.Errorf("grant %q must look like resource:action", spec)
	}
	return apikeys.Grant{Effect: "allow", Resource: resource, Action: action}, nil
}

func openService() (*apikeys.Service, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	queries := persistence.New(pool)
	svc := apikeys.NewService(persistence.NewAPIKeysRepo(queries, pool))
	return svc, func() { pool.Close() }, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()

	switch os.Args[1] {
	case "create":
		fs := flag.NewFlagSet("create", flag.ExitOnError)
		name := fs.String("name", "", "key name (required)")
		desc := fs.String("desc", "", "description")
		var grants stringList
		fs.Var(&grants, "grant", "grant spec resource:action (repeatable)")
		expires := fs.String("expires", "", "optional TTL duration, e.g. 720h")
		rps := fs.Int("rps", 60, "rate limit requests/second")
		_ = fs.Parse(os.Args[2:])

		if *name == "" || len(grants) == 0 {
			fmt.Fprintln(os.Stderr, "create requires --name and at least one --grant resource:action")
			os.Exit(2)
		}
		var specs []apikeys.GrantSpec
		for _, g := range grants {
			gr, err := parseGrant(g)
			if err != nil {
				log.Fatalf("%v", err)
			}
			specs = append(specs, apikeys.GrantSpec{Effect: "allow", Resource: gr.Resource, Action: gr.Action})
		}
		var exp *time.Time
		if *expires != "" {
			d, err := time.ParseDuration(*expires)
			if err != nil {
				log.Fatalf("invalid --expires: %v", err)
			}
			t := time.Now().Add(d)
			exp = &t
		}

		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()

		issued, err := svc.Create(ctx, *name, *desc, specs, exp, *rps)
		if err != nil {
			log.Fatalf("create: %v", err)
		}
		fmt.Printf("id:       %s\nprefix:   %s\nsecret:   %s\n\nStore the secret now — it is shown only once.\n",
			issued.ID, issued.KeyPrefix, issued.Secret)

	case "list":
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		keys, err := svc.List(ctx)
		if err != nil {
			log.Fatalf("list: %v", err)
		}
		for _, k := range keys {
			status := "active"
			if k.RevokedAt != nil {
				status = "REVOKED"
			} else if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now()) {
				status = "expired"
			}
			fmt.Printf("%-38s %-16s %-8s rps=%-4d last=%s expires=%s  %s\n",
				k.ID, k.KeyPrefix, status, k.RateLimitRPS,
				formatTime(k.LastUsedAt), formatTime(k.ExpiresAt), k.Name)
		}

	case "revoke":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		id, err := uuid.Parse(os.Args[2])
		if err != nil {
			log.Fatalf("invalid key id: %v", err)
		}
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		if err := svc.Revoke(ctx, id); err != nil {
			log.Fatalf("revoke: %v", err)
		}
		fmt.Println("revoked", id)

	case "grant":
		if len(os.Args) < 4 {
			usage()
			os.Exit(2)
		}
		verb := os.Args[2] // add | rm
		id, err := uuid.Parse(os.Args[3])
		if err != nil {
			log.Fatalf("invalid key id: %v", err)
		}
		if len(os.Args) < 5 {
			usage()
			os.Exit(2)
		}
		gr, err := parseGrant(os.Args[4])
		if err != nil {
			log.Fatalf("%v", err)
		}
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		switch verb {
		case "add":
			if err := svc.AddGrant(ctx, id, gr); err != nil {
				log.Fatalf("grant add: %v", err)
			}
			fmt.Println("grant added:", os.Args[4])
		case "rm", "remove", "del", "delete":
			if err := svc.RemoveGrant(ctx, id, gr); err != nil {
				log.Fatalf("grant rm: %v", err)
			}
			fmt.Println("grant removed:", os.Args[4])
		default:
			usage()
			os.Exit(2)
		}

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `keyctl — manage scoped API keys for MCP/agent clients

  keyctl create --name NAME --grant resource:action [--grant ...] [--expires 720h] [--desc TEXT] [--rps 60]
  keyctl list
  keyctl revoke <key-id>
  keyctl grant add <key-id> resource:action
  keyctl grant rm <key-id> resource:action`)
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04")
}
