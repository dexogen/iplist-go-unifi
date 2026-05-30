package config

import (
	"errors"
	"fmt"
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
	DryRun      bool   `yaml:"dry_run"`
	MinEntries  int    `yaml:"min_entries"`
	MaxEntries  int    `yaml:"max_entries"`
	AllowEmpty  bool   `yaml:"allow_empty"`
	BackupDir   string `yaml:"backup_dir"`
	KeepBackups int    `yaml:"keep_backups"`
	StateFile   string `yaml:"state_file"`
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
	MinEntries *int  `yaml:"min_entries"`
	MaxEntries *int  `yaml:"max_entries"`
	AllowEmpty *bool `yaml:"allow_empty"`
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
		cfg.UniFi.Timeout = "20s"
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
