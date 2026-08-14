// Command admin is the standalone CLI tool for the single platform administrator.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"macocr/proxy/domain"
	"macocr/proxy/internal/config"
	pgrepo "macocr/proxy/internal/repository/postgres"
	redisrepo "macocr/proxy/internal/repository/redis"
	"macocr/proxy/internal/usecase/auth"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgRepo, err := pgrepo.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to PostgreSQL: %v\n", err)
		os.Exit(1)
	}
	defer pgRepo.Close()

	if err := pgRepo.Migrate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error applying schema migrations: %v\n", err)
		os.Exit(1)
	}

	userRepo := pgrepo.NewUserRepository(pgRepo.Pool())
	configRepo := pgrepo.NewAccountConfigRepository(pgRepo.Pool())
	apiKeyRepo := pgrepo.NewAPIKeyRepository(pgRepo.Pool())
	redisRepo, err := redisrepo.New(cfg.RedisURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error configuring Redis: %v\n", err)
		os.Exit(1)
	}
	defer redisRepo.Close()
	authSvc := auth.NewService(userRepo, configRepo, apiKeyRepo, redisRepo)

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "seed":
		handleSeed(ctx, userRepo, configRepo, args)
	case "set-password":
		handleSetPassword(ctx, userRepo, args)
	case "create-user":
		handleCreateUser(ctx, authSvc, args)
	case "list-users":
		handleListUsers(ctx, authSvc)
	case "set-limits":
		handleSetLimits(ctx, authSvc, args)
	case "reset-quota":
		handleResetQuota(ctx, authSvc, args)
	case "disable-user":
		handleSetUserDisabled(ctx, authSvc, args, true)
	case "enable-user":
		handleSetUserDisabled(ctx, authSvc, args, false)
	case "create-key":
		handleCreateKey(ctx, authSvc, args)
	case "list-keys":
		handleListKeys(ctx, authSvc, args)
	case "revoke-key":
		handleRevokeKey(ctx, authSvc, args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func handleSeed(ctx context.Context, users domain.UserRepository, configs domain.AccountConfigRepository, args []string) {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	email := fs.String("email", "admin@macocr.local", "Admin email address")
	password := fs.String("password", "", "Admin password (required)")
	_ = fs.Parse(args)

	if strings.TrimSpace(*password) == "" {
		fmt.Fprintln(os.Stderr, "Error: --password is required for seed")
		os.Exit(1)
	}

	hash, err := auth.HashPassword(*password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error hashing password: %v\n", err)
		os.Exit(1)
	}

	existing, err := users.GetByEmail(ctx, *email)
	if err == nil && existing != nil {
		existing.Role = domain.RoleAdmin
		existing.PasswordHash = hash
		existing.Disabled = false
		_, err := users.Update(ctx, existing)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error updating admin user: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ Admin user updated: ID=%d, Email=%s, Role=%s\n", existing.ID, existing.Email, existing.Role)
		return
	}

	u := &domain.User{
		Email:        *email,
		Role:         domain.RoleAdmin,
		PasswordHash: hash,
		Disabled:     false,
	}
	created, err := users.Create(ctx, u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating admin user: %v\n", err)
		os.Exit(1)
	}

	cfg, err := configs.GetByUserID(ctx, created.ID)
	if err == nil && cfg != nil {
		cfg.RateLimitRPM = 0
		cfg.DocQuota = 0
		_, _ = configs.Update(ctx, cfg)
	}

	fmt.Printf("✓ Single Admin successfully seeded:\n  ID:       %d\n  Email:    %s\n  Role:     %s\n",
		created.ID, created.Email, created.Role)
}

func handleSetPassword(ctx context.Context, users domain.UserRepository, args []string) {
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	email := fs.String("email", "", "Admin email address (required)")
	password := fs.String("password", "", "New password (required)")
	_ = fs.Parse(args)

	if *email == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "Error: both --email and --password are required")
		os.Exit(1)
	}

	u, err := users.GetByEmail(ctx, *email)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: user with email %q not found: %v\n", *email, err)
		os.Exit(1)
	}

	hash, err := auth.HashPassword(*password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error hashing password: %v\n", err)
		os.Exit(1)
	}

	u.PasswordHash = hash
	if _, err := users.Update(ctx, u); err != nil {
		fmt.Fprintf(os.Stderr, "Error updating password: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Password updated successfully for user ID=%d (%s)\n", u.ID, u.Email)
}

func handleCreateUser(ctx context.Context, svc *auth.Service, args []string) {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	email := fs.String("email", "", "User email address (required)")
	rate := fs.Int("rate", 60, "Rate limit in requests per minute (0 = unlimited)")
	quota := fs.Int64("quota", 0, "Doc quota limit per cycle (0 = unlimited)")
	_ = fs.Parse(args)

	if *email == "" {
		fmt.Fprintln(os.Stderr, "Error: --email is required")
		os.Exit(1)
	}

	u, err := svc.CreateUser(ctx, *email, domain.RoleUser, "", rate, quota)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating user: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ User created successfully:\n  ID:             %d\n  Email:          %s\n  Role:           %s\n  Rate Limit:     %d rpm\n  Doc Quota:      %d docs\n",
		u.ID, u.Email, u.Role, u.Config.RateLimitRPM, u.Config.DocQuota)
}

func handleListUsers(ctx context.Context, svc *auth.Service) {
	users, err := svc.ListUsers(ctx, 1000, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing users: %v\n", err)
		os.Exit(1)
	}

	if len(users) == 0 {
		fmt.Println("No users found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 4, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tEMAIL\tROLE\tSTATUS\tRATE(RPM)\tDOC QUOTA\tDOC USED\tCREATED AT")
	fmt.Fprintln(w, "--\t-----\t----\t------\t---------\t---------\t--------\t----------")

	for _, u := range users {
		status := "Active"
		if u.Disabled {
			status = "Disabled"
		}
		rpm := 60
		quota := int64(0)
		used := int64(0)
		if u.Config != nil {
			rpm = u.Config.RateLimitRPM
			quota = u.Config.DocQuota
			used = u.Config.DocUsed
		}

		quotaStr := "unlimited"
		if quota > 0 {
			quotaStr = fmt.Sprintf("%d", quota)
		}
		rpmStr := "unlimited"
		if rpm > 0 {
			rpmStr = fmt.Sprintf("%d", rpm)
		}

		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			u.ID, u.Email, u.Role, status, rpmStr, quotaStr, used, u.CreatedAt.Format("2006-01-02 15:04"))
	}
	_ = w.Flush()
}

func handleSetLimits(ctx context.Context, svc *auth.Service, args []string) {
	fs := flag.NewFlagSet("set-limits", flag.ExitOnError)
	userID := fs.Int64("user-id", 0, "User ID (required)")
	rate := fs.Int("rate", -1, "New rate limit rpm (-1 to leave unchanged, 0 for unlimited)")
	quota := fs.Int64("quota", -1, "New doc quota (-1 to leave unchanged, 0 for unlimited)")
	_ = fs.Parse(args)

	if *userID <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --user-id is required")
		os.Exit(1)
	}

	var ratePtr *int
	if *rate >= 0 {
		ratePtr = rate
	}
	var quotaPtr *int64
	if *quota >= 0 {
		quotaPtr = quota
	}

	if ratePtr == nil && quotaPtr == nil {
		fmt.Fprintln(os.Stderr, "Error: specify at least one of --rate or --quota")
		os.Exit(1)
	}

	cfg, err := svc.UpdateAccountConfig(ctx, *userID, ratePtr, quotaPtr, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating limits: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ Limits updated for User ID=%d:\n  Rate Limit: %d rpm\n  Doc Quota:  %d docs\n  Doc Used:   %d docs\n",
		cfg.UserID, cfg.RateLimitRPM, cfg.DocQuota, cfg.DocUsed)
}

func handleResetQuota(ctx context.Context, svc *auth.Service, args []string) {
	fs := flag.NewFlagSet("reset-quota", flag.ExitOnError)
	userID := fs.Int64("user-id", 0, "User ID (required)")
	_ = fs.Parse(args)

	if *userID <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --user-id is required")
		os.Exit(1)
	}

	if err := svc.ResetDocQuota(ctx, *userID); err != nil {
		fmt.Fprintf(os.Stderr, "Error resetting quota: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Doc quota counter successfully reset (doc_used = 0) for User ID=%d\n", *userID)
}

func handleSetUserDisabled(ctx context.Context, svc *auth.Service, args []string, disabled bool) {
	actionName := "disable-user"
	if !disabled {
		actionName = "enable-user"
	}
	fs := flag.NewFlagSet(actionName, flag.ExitOnError)
	userID := fs.Int64("user-id", 0, "User ID (required)")
	_ = fs.Parse(args)

	if *userID <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --user-id is required")
		os.Exit(1)
	}

	u, err := svc.UpdateUser(ctx, *userID, nil, nil, &disabled)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating user status: %v\n", err)
		os.Exit(1)
	}

	status := "ENABLED"
	if u.Disabled {
		status = "DISABLED"
	}
	fmt.Printf("✓ User ID=%d (%s) is now %s\n", u.ID, u.Email, status)
}

func handleCreateKey(ctx context.Context, svc *auth.Service, args []string) {
	fs := flag.NewFlagSet("create-key", flag.ExitOnError)
	userID := fs.Int64("user-id", 0, "User ID (required)")
	name := fs.String("name", "default", "Key name identifier")
	rate := fs.Int("rate", 60, "Rate limit rpm for this key")
	_ = fs.Parse(args)

	if *userID <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --user-id is required")
		os.Exit(1)
	}

	k, err := svc.GenerateKey(ctx, *userID, *name, *rate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating key: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ API Key created for User ID=%d:\n  Key ID:     %d\n  Name:       %s\n  Rate Limit: %d rpm\n  API Key:    %s\n\n  ⚠️  Save this key now. It will NEVER be shown again in plaintext.\n",
		k.UserID, k.KeyID, k.Name, k.RateLimitRPM, k.Key)
}

func handleListKeys(ctx context.Context, svc *auth.Service, args []string) {
	fs := flag.NewFlagSet("list-keys", flag.ExitOnError)
	userID := fs.Int64("user-id", 0, "User ID (required)")
	_ = fs.Parse(args)

	if *userID <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --user-id is required")
		os.Exit(1)
	}

	keys, err := svc.ListKeys(ctx, *userID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing keys: %v\n", err)
		os.Exit(1)
	}

	if len(keys) == 0 {
		fmt.Printf("No API keys found for User ID=%d.\n", *userID)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 4, 8, 2, ' ', 0)
	fmt.Fprintln(w, "KEY ID\tNAME\tPREFIX\tRATE(RPM)\tSTATUS\tCREATED AT")
	fmt.Fprintln(w, "------\t----\t------\t---------\t------\t----------")

	for _, k := range keys {
		status := "Active"
		if k.RevokedAt != nil {
			status = fmt.Sprintf("Revoked (%s)", k.RevokedAt.Format("2006-01-02"))
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\n",
			k.ID, k.Name, k.Prefix, k.RateLimitRPM, status, k.CreatedAt.Format("2006-01-02 15:04"))
	}
	_ = w.Flush()
}

func handleRevokeKey(ctx context.Context, svc *auth.Service, args []string) {
	fs := flag.NewFlagSet("revoke-key", flag.ExitOnError)
	userID := fs.Int64("user-id", 0, "Owner user ID (required)")
	keyID := fs.Int64("key-id", 0, "API Key ID (required)")
	_ = fs.Parse(args)

	if *userID <= 0 || *keyID <= 0 {
		fmt.Fprintln(os.Stderr, "Error: --user-id and --key-id are required")
		os.Exit(1)
	}

	if err := svc.RevokeKey(ctx, *userID, *keyID); err != nil {
		fmt.Fprintf(os.Stderr, "Error revoking key: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ API Key ID=%d has been revoked.\n", *keyID)
}

func printUsage() {
	fmt.Println(`OCR Platform Admin CLI

Usage:
  go run ./cmd/admin <command> [options]

Commands:
  seed          Seed or reset the single Admin account
                --email <email> --password <password>

  set-password  Update password for an admin user
                --email <email> --password <new-password>

  create-user   Create a new user account with initial limits
                --email <email> [--rate <rpm>] [--quota <docs>]

  list-users    List all user accounts and their current limits & quota

  set-limits    Update rate limit and doc quota for a user
                --user-id <id> [--rate <rpm>] [--quota <docs>]

  reset-quota   Reset processed document count (doc_used = 0) for a user
                --user-id <id>

  disable-user  Block a user account from executing requests or creating keys
                --user-id <id>

  enable-user   Re-enable a blocked user account
                --user-id <id>

  create-key    Generate an API key for a user (plaintext printed once)
                --user-id <id> [--name <name>] [--rate <rpm>]

  list-keys     List all API keys belonging to a user
                --user-id <id>

  revoke-key    Immediately revoke an API key
                --user-id <owner-id> --key-id <id>`)
}
