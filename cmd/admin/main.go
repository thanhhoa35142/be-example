package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/thanhhoa35142/be-example/internal/infrastructure/persistence"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
	"github.com/thanhhoa35142/be-example/internal/platform/config"
)

func openService() (*adminauth.Service, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	// Challenges live in Redis; fall back to memory only when REDIS_URL is
	// unset. A malformed URL is a config error — fail loudly instead of
	// silently operating on a store the API never reads.
	var store adminauth.ChallengeStore = adminauth.NewMemoryChallengeStore()
	cleanup := func() { pool.Close() }
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			pool.Close()
			return nil, nil, fmt.Errorf("redis url: %w", err)
		}
		rdb := redis.NewClient(opts)
		cleanup = func() { _ = rdb.Close(); pool.Close() }
		store = persistence.NewAdminAuthChallengeStore(rdb)
		pctx, pcancel := context.WithTimeout(ctx, 3*time.Second)
		if err := rdb.Ping(pctx).Err(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: redis unreachable (%v) — challenge cleanup may fail\n", err)
		}
		pcancel()
	}
	svc, err := adminauth.NewService(persistence.NewAdminAuthRepo(persistence.New(pool)), store, nil, adminauth.DefaultConfig())
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return svc, cleanup, nil
}

func emailFlag(fs *flag.FlagSet) *string {
	return fs.String("email", "", "account email (required)")
}

func readPassword(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	fmt.Fprint(os.Stderr, "Password (min 12 chars): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx := context.Background()

	switch os.Args[1] {
	case "create-user":
		fs := flag.NewFlagSet("create-user", flag.ExitOnError)
		email := emailFlag(fs)
		password := fs.String("password", "", "initial password (prompted on stdin if omitted)")
		_ = fs.Parse(os.Args[2:])
		if *email == "" {
			fmt.Fprintln(os.Stderr, "create-user requires --email")
			os.Exit(2)
		}
		pw, err := readPassword(*password)
		if err != nil {
			log.Fatalf("read password: %v", err)
		}
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		u, err := svc.CreateUser(ctx, *email, pw)
		if err != nil {
			log.Fatalf("create-user: %v", err)
		}
		fmt.Printf("created %s (%s) — first login will require TOTP enrollment\n", u.Email, u.ID)

	case "set-password":
		fs := flag.NewFlagSet("set-password", flag.ExitOnError)
		email := emailFlag(fs)
		password := fs.String("password", "", "new password (prompted on stdin if omitted)")
		_ = fs.Parse(os.Args[2:])
		if *email == "" {
			fmt.Fprintln(os.Stderr, "set-password requires --email")
			os.Exit(2)
		}
		pw, err := readPassword(*password)
		if err != nil {
			log.Fatalf("read password: %v", err)
		}
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		if err := svc.SetPassword(ctx, *email, pw); err != nil {
			log.Fatalf("set-password: %v", err)
		}
		fmt.Println("password set for", *email, "— all sessions signed out")

	case "reset-2fa":
		fs := flag.NewFlagSet("reset-2fa", flag.ExitOnError)
		email := emailFlag(fs)
		_ = fs.Parse(os.Args[2:])
		if *email == "" {
			fmt.Fprintln(os.Stderr, "reset-2fa requires --email")
			os.Exit(2)
		}
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		if err := svc.Reset2FA(ctx, *email); err != nil {
			log.Fatalf("reset-2fa: %v", err)
		}
		fmt.Println("2FA reset for", *email, "— next login enrolls a new authenticator")

	case "disable-user", "enable-user":
		fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		email := emailFlag(fs)
		_ = fs.Parse(os.Args[2:])
		if *email == "" {
			fmt.Fprintln(os.Stderr, os.Args[1]+" requires --email")
			os.Exit(2)
		}
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		disabled := os.Args[1] == "disable-user"
		if err := svc.SetDisabled(ctx, *email, disabled); err != nil {
			log.Fatalf("%s: %v", os.Args[1], err)
		}
		state := "disabled"
		if !disabled {
			state = "enabled"
		}
		fmt.Println(state, *email)

	case "list":
		svc, closeFn, err := openService()
		if err != nil {
			log.Fatalf("db: %v", err)
		}
		defer closeFn()
		users, err := svc.ListUsers(ctx)
		if err != nil {
			log.Fatalf("list: %v", err)
		}
		for _, u := range users {
			state := "active"
			if u.DisabledAt != nil {
				state = "DISABLED"
			}
			totpState := "totp"
			if u.TotpConfirmedAt == nil {
				totpState = "no-2fa"
			}
			last := "-"
			if u.LastLoginAt != nil {
				last = u.LastLoginAt.UTC().Format("2006-01-02 15:04")
			}
			fmt.Printf("%-32s %-8s %-6s last-login %s\n", u.Email, state, totpState, last)
		}

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `admin — manage admin-console accounts (no self sign-up)

  admin create-user --email EMAIL [--password PASS]   # prompts for password on stdin if omitted
  admin set-password --email EMAIL [--password PASS]  # forgotten password; signs the user out everywhere
  admin reset-2fa --email EMAIL                      # force TOTP re-enrollment
  admin disable-user --email EMAIL                   # block login, kill sessions
  admin enable-user --email EMAIL
  admin list`)
}
