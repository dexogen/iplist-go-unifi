package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dexogen/iplist-go-unifi/internal/config"
	"github.com/dexogen/iplist-go-unifi/internal/iplist"
	reconcilesync "github.com/dexogen/iplist-go-unifi/internal/sync"
	"github.com/dexogen/iplist-go-unifi/internal/unificlient"
	"github.com/robfig/cron/v3"
)

type InspectOutput struct {
	Networks []InspectNetwork `json:"networks"`
	Routes   []InspectRoute   `json:"traffic_routes"`
}

type InspectNetwork struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Purpose string `json:"purpose,omitempty"`
}

type InspectRoute struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	Enabled        *bool  `json:"enabled,omitempty"`
	MatchingTarget string `json:"matching_target,omitempty"`
	NetworkID      string `json:"network_id,omitempty"`
	IPAddresses    int    `json:"ip_addresses"`
	Domains        int    `json:"domains"`
	IPRanges       int    `json:"ip_ranges"`
	Regions        int    `json:"regions"`
}

type Service struct {
	cfg    config.Config
	logger *slog.Logger
	mu     sync.RWMutex
	status reconcilesync.RunStatus
	runner *reconcilesync.Reconciler
}

func New(cfg config.Config, logger *slog.Logger) (*Service, error) {
	timeout, err := time.ParseDuration(cfg.UniFi.Timeout)
	if err != nil {
		return nil, err
	}
	client, err := unificlient.New(unificlient.Config{
		BaseURL:            cfg.UniFi.BaseURL,
		APIKey:             cfg.UniFi.APIKey,
		Username:           cfg.UniFi.Username,
		Password:           cfg.UniFi.Password,
		Site:               cfg.UniFi.Site,
		InsecureSkipVerify: cfg.UniFi.InsecureSkipVerify,
		Timeout:            timeout,
	})
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: timeout}
	service := &Service{
		cfg:    cfg,
		logger: logger,
	}
	service.runner = &reconcilesync.Reconciler{
		Config:  cfg,
		Client:  client,
		Fetcher: iplist.Fetcher{Client: httpClient},
		Logger:  logger,
	}
	return service, nil
}

func (s *Service) RunOnce(ctx context.Context) error {
	status, err := s.runner.Run(ctx)
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
	for _, source := range status.Sources {
		attrs := []any{
			"source", source.Name,
			"action", source.Action,
			"entries", source.Entries,
			"dry_run", source.DryRun,
		}
		if source.RouteID != "" {
			attrs = append(attrs, "route_id", source.RouteID)
		}
		if source.Added != 0 || source.Removed != 0 {
			attrs = append(attrs, "added", source.Added, "removed", source.Removed)
		}
		if source.Error != "" {
			attrs = append(attrs, "error", source.Error)
		}
		s.logger.Info("source sync finished", attrs...)
	}
	return err
}

func (s *Service) Inspect(ctx context.Context, w io.Writer) error {
	if err := s.runner.Client.Login(ctx); err != nil {
		return err
	}
	defer func() {
		if err := s.runner.Client.Logout(context.Background()); err != nil && s.logger != nil {
			s.logger.Warn("unifi logout failed", "error", err)
		}
	}()
	networks, err := s.runner.Client.ListNetworks(ctx)
	if err != nil {
		return fmt.Errorf("list networks: %w", err)
	}
	routes, err := s.runner.Client.ListTrafficRoutes(ctx)
	if err != nil {
		return fmt.Errorf("list traffic routes: %w", err)
	}
	out := InspectOutput{}
	for _, network := range networks {
		out.Networks = append(out.Networks, InspectNetwork{
			ID:      network.ID,
			Name:    network.Name,
			Purpose: network.Purpose,
		})
	}
	for _, route := range routes {
		out.Routes = append(out.Routes, InspectRoute{
			ID:             route.ID,
			Name:           route.Name,
			Description:    route.Description,
			Enabled:        route.Enabled,
			MatchingTarget: route.MatchingTarget,
			NetworkID:      route.NetworkID,
			IPAddresses:    len(route.IPAddresses),
			Domains:        len(route.Domains),
			IPRanges:       len(route.IPRanges),
			Regions:        len(route.Regions),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func (s *Service) Run(ctx context.Context) error {
	location, err := time.LoadLocation(s.cfg.Schedule.Timezone)
	if err != nil {
		return fmt.Errorf("load schedule timezone: %w", err)
	}
	scheduler := cron.New(cron.WithLocation(location))
	if _, err := scheduler.AddFunc(s.cfg.Schedule.Cron, func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := s.RunOnce(runCtx); err != nil {
			s.logger.Error("scheduled sync failed", "error", err)
		}
	}); err != nil {
		return fmt.Errorf("parse schedule cron: %w", err)
	}

	server := &http.Server{
		Addr:              s.cfg.Server.Listen,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("status server listening", "listen", s.cfg.Server.Listen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	scheduler.Start()
	defer scheduler.Stop()

	if s.cfg.Schedule.RunOnStart {
		go func() {
			runCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := s.RunOnce(runCtx); err != nil {
				s.logger.Error("startup sync failed", "error", err)
			}
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.RLock()
		status := s.status
		s.mu.RUnlock()
		if status.FinishedAt.IsZero() {
			http.Error(w, "no sync completed", http.StatusServiceUnavailable)
			return
		}
		if status.Error != "" {
			http.Error(w, status.Error, http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.RLock()
		status := s.status
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	})
	return mux
}
