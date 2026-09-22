package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	UniFi    UniFiConfig    `yaml:"unifi"`
	Schedule ScheduleConfig `yaml:"schedule"`
	Safety   SafetyConfig   `yaml:"safety"`
	Server   ServerConfig   `yaml:"server"`
	Sources  []SourceConfig `yaml:"sources"`
}

type UniFiConfig struct {
	BaseURL            string `yaml:"base_url"`
	APIKey             string `yaml:"api_key"`
	Username           string `yaml:"username"`
	Password           string `yaml:"password"`
	Site               string `yaml:"site"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	Timeout            string `yaml:"timeout"`
}

type ScheduleConfig struct {
	Cron       string `yaml:"cron"`
	Timezone   string `yaml:"timezone"`
	RunOnStart bool   `yaml:"run_on_start"`
}

type SafetyConfig struct {
	DryRun          bool     `yaml:"dry_run"`
	MinEntries      int      `yaml:"min_entries"`
	MaxEntries      int      `yaml:"max_entries"`
	AllowEmpty      bool     `yaml:"allow_empty"`
	BackupDir       string   `yaml:"backup_dir"`
	KeepBackups     int      `yaml:"keep_backups"`
	StateFile       string   `yaml:"state_file"`
	MaxRemovalRatio *float64 `yaml:"max_removal_ratio"`
	RequireFresh    bool     `yaml:"require_fresh"`
	MaxSourceAge    string   `yaml:"max_source_age"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type SourceConfig struct {
	Name          string             `yaml:"name"`
	URL           string             `yaml:"url"`
	Type          string             `yaml:"type"`
	NetworkID     string             `yaml:"network_id"`
	NetworkName   string             `yaml:"network_name"`
	KillSwitch    *bool              `yaml:"kill_switch"`
	TargetDevices []TargetDevice     `yaml:"target_devices"`
	AdoptExisting bool               `yaml:"adopt_existing"`
	Safety        SourceSafetyConfig `yaml:"safety"`
}

type TargetDevice struct {
	Type        string `yaml:"type"`
	ClientMAC   string `yaml:"client_mac"`
	NetworkID   string `yaml:"network_id"`
	NetworkName string `yaml:"network_name"`
}

type SourceSafetyConfig struct {
	MinEntries      *int     `yaml:"min_entries"`
	MaxEntries      *int     `yaml:"max_entries"`
	AllowEmpty      *bool    `yaml:"allow_empty"`
	MaxRemovalRatio *float64 `yaml:"max_removal_ratio"`
	RequireFresh    *bool    `yaml:"require_fresh"`
}

func Load(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg *Config) applyDefaults() {
	if cfg.UniFi.Site == "" {
		cfg.UniFi.Site = "default"
	}
	if cfg.UniFi.Timeout == "" {
		cfg.UniFi.Timeout = "120s"
	}
	if cfg.Schedule.Cron == "" {
		cfg.Schedule.Cron = "0 0 * * *"
	}
	if cfg.Schedule.Timezone == "" {
		cfg.Schedule.Timezone = "UTC"
	}
	if cfg.Safety.BackupDir == "" {
		cfg.Safety.BackupDir = "/var/lib/iplist-go-unifi/backups"
	}
	if cfg.Safety.StateFile == "" {
		cfg.Safety.StateFile = "/var/lib/iplist-go-unifi/state/routes.json"
	}
	if cfg.Safety.KeepBackups == 0 {
		cfg.Safety.KeepBackups = 20
	}
	if cfg.Safety.MaxEntries == 0 {
		cfg.Safety.MaxEntries = 20000
	}
	if cfg.Safety.MaxSourceAge == "" {
		cfg.Safety.MaxSourceAge = "48h"
	}
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = ":18086"
	}
	for i := range cfg.Sources {
		if cfg.Sources[i].Type == "" {
			cfg.Sources[i].Type = "ipv4_cidr"
		}
	}
}

func (cfg Config) Validate() error {
	if cfg.UniFi.BaseURL == "" {
		return errors.New("unifi.base_url is required")
	}
	if _, err := url.ParseRequestURI(cfg.UniFi.BaseURL); err != nil {
		return fmt.Errorf("unifi.base_url is invalid: %w", err)
	}
	if _, err := time.ParseDuration(cfg.UniFi.Timeout); err != nil {
		return fmt.Errorf("unifi.timeout is invalid: %w", err)
	}
	if cfg.UniFi.APIKey == "" && (cfg.UniFi.Username == "" || cfg.UniFi.Password == "") {
		return errors.New("unifi.api_key or unifi.username plus unifi.password is required")
	}
	if cfg.UniFi.APIKey != "" && (cfg.UniFi.Username != "" || cfg.UniFi.Password != "") {
		return errors.New("use either unifi.api_key or username/password, not both")
	}
	if cfg.Safety.MinEntries < 0 || cfg.Safety.MaxEntries < 0 {
		return errors.New("safety entry limits must be non-negative")
	}
	if cfg.Safety.MaxEntries > 0 && cfg.Safety.MinEntries > cfg.Safety.MaxEntries {
		return errors.New("safety.min_entries cannot be greater than safety.max_entries")
	}
	if len(cfg.Sources) == 0 {
		return errors.New("at least one source is required")
	}
	if err := validateRatio(cfg.Safety.MaxRemovalRatio); err != nil {
		return err
	}
	if cfg.Safety.MaxSourceAge != "" {
		if age, err := time.ParseDuration(cfg.Safety.MaxSourceAge); err != nil || age <= 0 {
			return errors.New("safety.max_source_age must be a positive duration")
		}
	}
	names := map[string]struct{}{}
	for i, source := range cfg.Sources {
		if err := source.Validate(); err != nil {
			return fmt.Errorf("sources[%d]: %w", i, err)
		}
		if _, ok := names[source.Name]; ok {
			return fmt.Errorf("sources[%d]: duplicate source name %q", i, source.Name)
		}
		names[source.Name] = struct{}{}
	}
	return nil
}

func (s SourceConfig) Validate() error {
	if err := validateRatio(s.Safety.MaxRemovalRatio); err != nil {
		return err
	}
	if s.Name == "" {
		return errors.New("name is required")
	}
	if s.URL == "" {
		return errors.New("url is required")
	}
	if _, err := url.ParseRequestURI(s.URL); err != nil {
		return fmt.Errorf("url is invalid: %w", err)
	}
	switch s.Type {
	case "ipv4_cidr", "ipv6_cidr", "ip_cidr", "domains":
	default:
		return fmt.Errorf("unsupported type %q", s.Type)
	}
	if s.NetworkID == "" && s.NetworkName == "" {
		return errors.New("network_id or network_name is required")
	}
	return nil
}

func validateRatio(value *float64) error {
	if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 1) {
		return errors.New("max_removal_ratio must be between 0 and 1")
	}
	return nil
}

func (s SourceConfig) RemovalLimit(global *float64) float64 {
	if s.Safety.MaxRemovalRatio != nil {
		return *s.Safety.MaxRemovalRatio
	}
	if global != nil {
		return *global
	}
	return .35
}

func (s SourceConfig) RequiresFresh(global bool) bool {
	if s.Safety.RequireFresh != nil {
		return *s.Safety.RequireFresh
	}
	return global
}

func (s SourceConfig) MinEntries(global int) int {
	if s.Safety.MinEntries != nil {
		return *s.Safety.MinEntries
	}
	return global
}

func (s SourceConfig) MaxEntries(global int) int {
	if s.Safety.MaxEntries != nil {
		return *s.Safety.MaxEntries
	}
	return global
}

func (s SourceConfig) AllowEmpty(global bool) bool {
	if s.Safety.AllowEmpty != nil {
		return *s.Safety.AllowEmpty
	}
	return global
}
