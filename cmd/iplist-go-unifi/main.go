package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dexogen/iplist-go-unifi/internal/app"
	"github.com/dexogen/iplist-go-unifi/internal/config"
)

func main() {
	var (
		configPath = flag.String("config", env("IPLIST_UNIFI_CONFIG", "/etc/iplist-go-unifi/config.yml"), "path to YAML config")
		once       = flag.Bool("once", false, "run one sync and exit")
		dryRun     = flag.Bool("dry-run", false, "show changes without writing to UniFi")
		inspect    = flag.Bool("inspect", false, "print UniFi networks and traffic routes without changing anything")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel(env("IPLIST_UNIFI_LOG_LEVEL", "info")),
	}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}
	if *dryRun {
		cfg.Safety.DryRun = true
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	service, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("initialize service failed", "error", err)
		os.Exit(1)
	}

	if *once {
		if err := service.RunOnce(ctx); err != nil {
			logger.Error("sync failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if *inspect {
		if err := service.Inspect(ctx, os.Stdout); err != nil {
			logger.Error("inspect failed", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := service.Run(ctx); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func logLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		fmt.Fprintf(os.Stderr, "unknown log level %q, using info\n", value)
		return slog.LevelInfo
	}
}
