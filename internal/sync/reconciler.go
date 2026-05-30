package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dexogen/iplist-go-unifi/internal/config"
	"github.com/dexogen/iplist-go-unifi/internal/iplist"
	"github.com/dexogen/iplist-go-unifi/internal/unificlient"
	"github.com/resnickio/unifi-go-sdk/pkg/unifi"
)

const owner = "iplist-go-unifi"

type Reconciler struct {
	Config  config.Config
	Client  unificlient.Client
	Fetcher iplist.Fetcher
	Logger  *slog.Logger
}

func (r *Reconciler) Run(ctx context.Context) (RunStatus, error) {
	status := RunStatus{StartedAt: time.Now().UTC()}
	err := r.Client.Login(ctx)
	if err != nil {
		status.FinishedAt = time.Now().UTC()
		status.Error = err.Error()
		return status, err
	}
	defer func() {
		if err := r.Client.Logout(context.Background()); err != nil && r.Logger != nil {
			r.Logger.Warn("unifi logout failed", "error", err)
		}
	}()

	networks, err := r.Client.ListNetworks(ctx)
	if err != nil {
		status.FinishedAt = time.Now().UTC()
		status.Error = err.Error()
		return status, fmt.Errorf("list networks: %w", err)
	}

	routes, err := r.Client.ListTrafficRoutes(ctx)
	if err != nil {
		status.FinishedAt = time.Now().UTC()
		status.Error = err.Error()
		return status, fmt.Errorf("list traffic routes: %w", err)
	}

	for _, source := range r.Config.Sources {
		sourceStatus := r.reconcileSource(ctx, source, networks, routes)
		status.Sources = append(status.Sources, sourceStatus)
		if sourceStatus.Error != "" {
			err = errors.Join(err, fmt.Errorf("%s: %s", source.Name, sourceStatus.Error))
			continue
		}
		if sourceStatus.Action == "created" || sourceStatus.Action == "updated" {
			routes, _ = r.Client.ListTrafficRoutes(ctx)
		}
	}
	status.FinishedAt = time.Now().UTC()
	if err != nil {
		status.Error = err.Error()
	}
	return status, err
}

func (r *Reconciler) reconcileSource(ctx context.Context, source config.SourceConfig, networks []unifi.Network, routes []unifi.TrafficRoute) SourceStatus {
	st := SourceStatus{Name: source.Name, DryRun: r.Config.Safety.DryRun}
	result, err := r.Fetcher.Fetch(ctx, source.URL, source.Type)
	if err != nil {
		st.Action = "failed"
		st.Error = err.Error()
		return st
	}
	st.Entries = len(result.Values)
	st.Hash = result.Hash

	if err := r.validateSafety(source, len(result.Values)); err != nil {
		st.Action = "blocked"
		st.Error = err.Error()
		return st
	}

	networkID, err := resolveNetworkID(source.NetworkID, source.NetworkName, networks)
	if err != nil {
		st.Action = "failed"
		st.Error = err.Error()
		return st
	}
	targets, err := resolveTargets(source.TargetDevices, networks)
	if err != nil {
		st.Action = "failed"
		st.Error = err.Error()
		return st
	}
	if len(targets) == 0 {
		targets = []unifi.TrafficRuleTarget{{Type: "ALL_CLIENTS"}}
	}

	current := findManagedRoute(routes, source.Name)
	if current == nil {
		unmanaged := findUnmanagedRoute(routes, source)
		if unmanaged != nil {
			if !source.AdoptExisting {
				st.Action = "blocked"
				st.RouteID = unmanaged.ID
				st.Error = "matching unmanaged UniFi traffic route exists; set adopt_existing=true to take ownership"
				return st
			}
			current = unmanaged
		}
	}
	desired := desiredRoute(source, result.Values, result.Hash, networkID, targets)
	if current == nil {
		st.Action = "create"
		if r.Config.Safety.DryRun {
			return st
		}
		created, err := r.Client.CreateTrafficRoute(ctx, desired)
		if err != nil {
			st.Action = "failed"
			st.Error = err.Error()
			return st
		}
		st.Action = "created"
		st.RouteID = created.ID
		return st
	}

	st.RouteID = current.ID
	currentHash := routeHash(current.Description)
	if currentHash != "" && strings.HasPrefix(result.Hash, currentHash) && routeEquivalent(*current, desired) {
		st.Action = "unchanged"
		st.Unchanged = true
		return st
	}

	st.Added, st.Removed = routeDiffCounts(*current, desired)
	if r.Config.Safety.DryRun {
		st.Action = "update"
		return st
	}

	backupPath, err := r.backupRoute(source.Name, *current)
	if err != nil {
		st.Action = "failed"
		st.Error = err.Error()
		return st
	}
	st.Backup = backupPath
	st.Action = "update"
	updated, err := r.Client.UpdateTrafficRoute(ctx, current.ID, desired)
	if err != nil {
		st.Action = "failed"
		st.Error = err.Error()
		return st
	}
	st.Action = "updated"
	st.RouteID = updated.ID
	return st
}

func (r *Reconciler) validateSafety(source config.SourceConfig, count int) error {
	if count == 0 && !source.AllowEmpty(r.Config.Safety.AllowEmpty) {
		return errors.New("source returned no entries and empty updates are disabled")
	}
	if min := source.MinEntries(r.Config.Safety.MinEntries); min > 0 && count < min {
		return fmt.Errorf("source returned %d entries, below minimum %d", count, min)
	}
	if max := source.MaxEntries(r.Config.Safety.MaxEntries); max > 0 && count > max {
		return fmt.Errorf("source returned %d entries, above maximum %d", count, max)
	}
	return nil
}

func desiredRoute(source config.SourceConfig, values []string, hash, networkID string, targets []unifi.TrafficRuleTarget) *unifi.TrafficRoute {
	enabled := true
	route := &unifi.TrafficRoute{
		Name:              source.Name,
		Enabled:           &enabled,
		Description:       marker(source.Name, hash),
		NetworkID:         networkID,
		TargetDevices:     targets,
		KillSwitchEnabled: source.KillSwitch,
	}
	if source.Type == "domains" {
		route.MatchingTarget = "DOMAIN"
		for _, value := range values {
			route.Domains = append(route.Domains, unifi.TrafficDomain{Domain: value})
		}
	} else {
		route.MatchingTarget = "IP"
		route.IPAddresses = append(route.IPAddresses, values...)
	}
	return route
}

func findManagedRoute(routes []unifi.TrafficRoute, sourceName string) *unifi.TrafficRoute {
	for i := range routes {
		if routeOwner(routes[i].Description) == owner && routeSource(routes[i].Description) == sourceName {
			return &routes[i]
		}
	}
	return nil
}

func findUnmanagedRoute(routes []unifi.TrafficRoute, source config.SourceConfig) *unifi.TrafficRoute {
	for i := range routes {
		if routeOwner(routes[i].Description) == owner {
			continue
		}
		if routes[i].Description == source.Name {
			return &routes[i]
		}
	}
	return nil
}

func routeEquivalent(current unifi.TrafficRoute, desired *unifi.TrafficRoute) bool {
	if current.MatchingTarget != desired.MatchingTarget || current.NetworkID != desired.NetworkID {
		return false
	}
	if !sameBool(current.Enabled, desired.Enabled) || !sameBool(current.KillSwitchEnabled, desired.KillSwitchEnabled) {
		return false
	}
	if !sameTargets(current.TargetDevices, desired.TargetDevices) {
		return false
	}
	if desired.MatchingTarget == "DOMAIN" {
		return sameDomains(current.Domains, desired.Domains)
	}
	return sameStrings(current.IPAddresses, desired.IPAddresses)
}

func sameBool(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameStrings(a, b []string) bool {
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	if len(aa) != len(bb) {
		return false
	}
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func sameDomains(a, b []unifi.TrafficDomain) bool {
	aa := make([]string, 0, len(a))
	bb := make([]string, 0, len(b))
	for _, item := range a {
		aa = append(aa, item.Domain)
	}
	for _, item := range b {
		bb = append(bb, item.Domain)
	}
	return sameStrings(aa, bb)
}

func sameTargets(a, b []unifi.TrafficRuleTarget) bool {
	aa := make([]string, 0, len(a))
	bb := make([]string, 0, len(b))
	for _, item := range a {
		aa = append(aa, item.Type+"|"+item.ClientMAC+"|"+item.NetworkID)
	}
	for _, item := range b {
		bb = append(bb, item.Type+"|"+item.ClientMAC+"|"+item.NetworkID)
	}
	return sameStrings(aa, bb)
}

func routeDiffCounts(current unifi.TrafficRoute, desired *unifi.TrafficRoute) (int, int) {
	if desired.MatchingTarget == "DOMAIN" {
		currentValues := make([]string, 0, len(current.Domains))
		desiredValues := make([]string, 0, len(desired.Domains))
		for _, item := range current.Domains {
			currentValues = append(currentValues, item.Domain)
		}
		for _, item := range desired.Domains {
			desiredValues = append(desiredValues, item.Domain)
		}
		return diffCounts(currentValues, desiredValues)
	}
	return diffCounts(current.IPAddresses, desired.IPAddresses)
}

func diffCounts(current, desired []string) (int, int) {
	currentSet := map[string]struct{}{}
	for _, value := range current {
		currentSet[value] = struct{}{}
	}
	desiredSet := map[string]struct{}{}
	for _, value := range desired {
		desiredSet[value] = struct{}{}
	}
	var added, removed int
	for value := range desiredSet {
		if _, ok := currentSet[value]; !ok {
			added++
		}
	}
	for value := range currentSet {
		if _, ok := desiredSet[value]; !ok {
			removed++
		}
	}
	return added, removed
}

func resolveNetworkID(id, name string, networks []unifi.Network) (string, error) {
	if id != "" {
		return id, nil
	}
	for _, network := range networks {
		if network.Name == name {
			return network.ID, nil
		}
	}
	return "", fmt.Errorf("network %q not found", name)
}

func resolveTargets(targets []config.TargetDevice, networks []unifi.Network) ([]unifi.TrafficRuleTarget, error) {
	result := make([]unifi.TrafficRuleTarget, 0, len(targets))
	for _, target := range targets {
		t := unifi.TrafficRuleTarget{
			Type:      target.Type,
			ClientMAC: target.ClientMAC,
			NetworkID: target.NetworkID,
		}
		if t.Type == "" {
			t.Type = "ALL_CLIENTS"
		}
		if target.NetworkName != "" {
			id, err := resolveNetworkID("", target.NetworkName, networks)
			if err != nil {
				return nil, err
			}
			t.NetworkID = id
		}
		result = append(result, t)
	}
	return result, nil
}

func marker(sourceName, hash string) string {
	shortHash := hash
	if len(shortHash) > 16 {
		shortHash = shortHash[:16]
	}
	return fmt.Sprintf("managed-by=%s source=%s hash=sha256:%s", owner, sourceName, shortHash)
}

func routeOwner(description string) string {
	return markerField(description, "managed-by")
}

func routeSource(description string) string {
	return markerField(description, "source")
}

func routeHash(description string) string {
	value := markerField(description, "hash")
	return strings.TrimPrefix(value, "sha256:")
}

func markerField(description, key string) string {
	for _, field := range strings.Fields(description) {
		name, value, ok := strings.Cut(field, "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

func (r *Reconciler) backupRoute(sourceName string, route unifi.TrafficRoute) (string, error) {
	if r.Config.Safety.BackupDir == "" {
		return "", nil
	}
	if err := os.MkdirAll(r.Config.Safety.BackupDir, 0o750); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s.json", time.Now().UTC().Format("20060102T150405Z"), sourceName, route.ID)
	path := filepath.Join(r.Config.Safety.BackupDir, name)
	body, err := json.MarshalIndent(route, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	r.pruneBackups()
	return path, nil
}

func (r *Reconciler) pruneBackups() {
	keep := r.Config.Safety.KeepBackups
	if keep <= 0 || r.Config.Safety.BackupDir == "" {
		return
	}
	entries, err := os.ReadDir(r.Config.Safety.BackupDir)
	if err != nil {
		return
	}
	var files []os.DirEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			files = append(files, entry)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() > files[j].Name() })
	if len(files) <= keep {
		return
	}
	for _, entry := range files[keep:] {
		_ = os.Remove(filepath.Join(r.Config.Safety.BackupDir, entry.Name()))
	}
}
