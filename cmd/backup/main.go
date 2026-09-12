// Command backup dumps or restores control-plane PostgreSQL.
//
//	go run ./cmd/backup
//	go run ./cmd/backup -restore backups/forge-….sql
//
// Redis is not included. A Redis restart drops queued jobs; deployment
// rows stay in SQL. Backup files are secrets — they stay out of git.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/reyansh7/Forge/internal/backup"
	"github.com/reyansh7/Forge/internal/config"
)

func main() {
	restore := flag.String("restore", "", "SQL file to restore (destructive)")
	out := flag.String("out", "", "dump destination (default backups/forge-<utc>.sql)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if *restore != "" {
		if err := backup.Restore(ctx, cfg.DatabaseURL, *restore); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("restored", *restore)
		return
	}
	dest := *out
	if dest == "" {
		dest = backup.DefaultName()
	}
	if err := backup.Dump(ctx, cfg.DatabaseURL, dest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", dest)
}
