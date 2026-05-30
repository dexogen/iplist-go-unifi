package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dexogen/iplist-go-unifi/internal/config"
	"github.com/dexogen/iplist-go-unifi/internal/iplist"
	"github.com/resnickio/unifi-go-sdk/pkg/unifi"
)

type fakeUniFi struct {
	networks []unifi.Network
	routes   []unifi.TrafficRoute
	created  *unifi.TrafficRoute
	updated  *unifi.TrafficRoute
}

func (f *fakeUniFi) Login(context.Context) error  { return nil }
func (f *fakeUniFi) Logout(context.Context) error { return nil }
func (f *fakeUniFi) ListNetworks(context.Context) ([]unifi.Network, error) {
	return f.networks, nil
}
func (f *fakeUniFi) ListTrafficRoutes(context.Context) ([]unifi.TrafficRoute, error) {
	return f.routes, nil
}
func (f *fakeUniFi) CreateTrafficRoute(_ context.Context, route *unifi.TrafficRoute) (*unifi.TrafficRoute, error) {
	copy := *route
	copy.ID = "created-id"
	f.created = &copy
	f.routes = append(f.routes, copy)
	return &copy, nil
}
func (f *fakeUniFi) UpdateTrafficRoute(_ context.Context, id string, route *unifi.TrafficRoute) (*unifi.TrafficRoute, error) {
	copy := *route
	copy.ID = id
	f.updated = &copy
	return &copy, nil
}

func TestRunCreatesManagedRoute(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("8.8.8.8\n"))
	}))
	defer sourceServer.Close()

	client := &fakeUniFi{
		networks: []unifi.Network{{ID: "wan-id", Name: "WAN"}},
	}
	cfg := testConfig(sourceServer.URL)
	r := &Reconciler{
		Config:  cfg,
		Client:  client,
		Fetcher: iplist.Fetcher{Client: sourceServer.Client()},
	}
	status, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Sources) != 1 || status.Sources[0].Action != "created" {
		t.Fatalf("status = %+v", status.Sources)
	}
	if client.created == nil {
		t.Fatal("route was not created")
	}
	if client.created.MatchingTarget != "IP" {
		t.Fatalf("matching target = %q", client.created.MatchingTarget)
	}
	if got := client.created.IPAddresses; len(got) != 1 || got[0] != "8.8.8.8/32" {
		t.Fatalf("ip addresses = %v", got)
	}
	if client.created.Description != "test" {
		t.Fatalf("description = %q", client.created.Description)
	}
}

func TestRunBlocksEmptySource(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer sourceServer.Close()

	client := &fakeUniFi{networks: []unifi.Network{{ID: "wan-id", Name: "WAN"}}}
	cfg := testConfig(sourceServer.URL)
	r := &Reconciler{
		Config:  cfg,
		Client:  client,
		Fetcher: iplist.Fetcher{Client: sourceServer.Client()},
	}
	status, err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected safety error")
	}
	if status.Sources[0].Action != "blocked" {
		t.Fatalf("action = %q", status.Sources[0].Action)
	}
	if client.created != nil {
		t.Fatal("empty source should not create route")
	}
}

func TestRunBlocksMatchingUnmanagedRoute(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("8.8.8.8\n"))
	}))
	defer sourceServer.Close()

	client := &fakeUniFi{
		networks: []unifi.Network{{ID: "wan-id", Name: "WAN"}},
		routes: []unifi.TrafficRoute{{
			ID:          "manual-id",
			Description: "test",
		}},
	}
	cfg := testConfig(sourceServer.URL)
	r := &Reconciler{
		Config:  cfg,
		Client:  client,
		Fetcher: iplist.Fetcher{Client: sourceServer.Client()},
	}
	status, err := r.Run(context.Background())
	if err == nil {
		t.Fatal("expected unmanaged route safety error")
	}
	if status.Sources[0].Action != "blocked" {
		t.Fatalf("action = %q", status.Sources[0].Action)
	}
	if client.created != nil || client.updated != nil {
		t.Fatal("unmanaged route should not be created or updated")
	}
}

func TestRunStoresRouteState(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("8.8.8.8\n"))
	}))
	defer sourceServer.Close()

	statePath := filepath.Join(t.TempDir(), "state", "routes.json")
	client := &fakeUniFi{
		networks: []unifi.Network{{ID: "wan-id", Name: "WAN"}},
	}
	cfg := testConfig(sourceServer.URL)
	cfg.Safety.StateFile = statePath
	r := &Reconciler{
		Config:  cfg,
		Client:  client,
		Fetcher: iplist.Fetcher{Client: sourceServer.Client()},
	}

	if _, err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state stateFile
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if state.Routes["test"].RouteID != "created-id" {
		t.Fatalf("route id = %q", state.Routes["test"].RouteID)
	}
	if state.Routes["test"].Hash == "" {
		t.Fatal("hash was not stored")
	}
}

func TestRunCleansLegacyManagedDescription(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("8.8.8.8\n"))
	}))
	defer sourceServer.Close()

	client := &fakeUniFi{
		networks: []unifi.Network{{ID: "wan-id", Name: "WAN"}},
		routes: []unifi.TrafficRoute{{
			ID:             "legacy-id",
			Description:    marker("test", "oldhash"),
			MatchingTarget: "IP",
			NetworkID:      "wan-id",
			IPAddresses:    []string{"8.8.8.8/32"},
		}},
	}
	cfg := testConfig(sourceServer.URL)
	cfg.Safety.StateFile = filepath.Join(t.TempDir(), "state", "routes.json")
	r := &Reconciler{
		Config:  cfg,
		Client:  client,
		Fetcher: iplist.Fetcher{Client: sourceServer.Client()},
	}

	status, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Sources[0].Action != "updated" {
		t.Fatalf("action = %q", status.Sources[0].Action)
	}
	if client.updated == nil {
		t.Fatal("legacy route was not updated")
	}
	if client.updated.Description != "test" {
		t.Fatalf("description = %q", client.updated.Description)
	}
}

func TestRunTreatsDefaultKillSwitchAsUnchanged(t *testing.T) {
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("8.8.8.8\n"))
	}))
	defer sourceServer.Close()

	enabled := true
	killSwitch := false
	statePath := filepath.Join(t.TempDir(), "state", "routes.json")
	if err := saveState(statePath, stateFile{Routes: map[string]routeState{
		"test": {RouteID: "route-id"},
	}}); err != nil {
		t.Fatal(err)
	}
	client := &fakeUniFi{
		networks: []unifi.Network{{ID: "wan-id", Name: "WAN"}},
		routes: []unifi.TrafficRoute{{
			ID:                "route-id",
			Enabled:           &enabled,
			Description:       "test",
			MatchingTarget:    "IP",
			NetworkID:         "wan-id",
			TargetDevices:     []unifi.TrafficRuleTarget{{Type: "ALL_CLIENTS"}},
			IPAddresses:       []string{"8.8.8.8/32"},
			KillSwitchEnabled: &killSwitch,
		}},
	}
	cfg := testConfig(sourceServer.URL)
	cfg.Safety.StateFile = statePath
	r := &Reconciler{
		Config:  cfg,
		Client:  client,
		Fetcher: iplist.Fetcher{Client: sourceServer.Client()},
	}

	status, err := r.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Sources[0].Action != "unchanged" {
		t.Fatalf("action = %q", status.Sources[0].Action)
	}
	if client.updated != nil {
		t.Fatal("unchanged route should not be updated")
	}
}

func TestPruneBackupsKeepsSmallBackupSet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"20260530T100000Z-test-one.json", "20260530T110000Z-test-two.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := &Reconciler{Config: config.Config{Safety: config.SafetyConfig{
		BackupDir:   dir,
		KeepBackups: 20,
	}}}

	r.pruneBackups()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("backup count = %d", len(entries))
	}
}

func testConfig(url string) config.Config {
	return config.Config{
		Safety: config.SafetyConfig{MaxEntries: 20000, BackupDir: "", StateFile: ""},
		Sources: []config.SourceConfig{{
			Name:      "test",
			URL:       url,
			Type:      "ipv4_cidr",
			NetworkID: "wan-id",
		}},
	}
}
