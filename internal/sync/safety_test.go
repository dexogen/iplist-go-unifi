package sync

import (
	"strings"
	"testing"
	"time"

	"github.com/dexogen/iplist-go-unifi/internal/config"
	"github.com/dexogen/iplist-go-unifi/internal/iplist"
	"github.com/resnickio/unifi-go-sdk/pkg/unifi"
)

func TestCoverageCheckAcceptsCIDRAggregationAndSplits(t *testing.T) {
	for _, test := range []struct {
		old, next []string
		want      int
	}{
		{[]string{"10.0.0.0/25", "10.0.0.128/25"}, []string{"10.0.0.0/24"}, 0},
		{[]string{"10.0.0.0/24"}, []string{"10.0.0.0/25", "10.0.0.128/25"}, 0},
		{[]string{"10.0.0.0/24"}, []string{"10.0.0.0/25"}, 1},
		{[]string{"1.1.1.1", "8.8.8.8"}, []string{"1.1.1.1/32"}, 1},
		{[]string{"2001:db8::/33", "2001:db8:8000::/33"}, []string{"2001:db8::/32"}, 0},
	} {
		got := uncoveredRoutes(unifi.TrafficRoute{IPAddresses: test.old}, &unifi.TrafficRoute{MatchingTarget: "IP", IPAddresses: test.next})
		if got != test.want {
			t.Errorf("%v -> %v: missing %d want %d", test.old, test.next, got, test.want)
		}
	}
}

func TestFreshnessBlocksMissingStaleDegradedAndFutureSources(t *testing.T) {
	r := Reconciler{Config: config.Config{Safety: config.SafetyConfig{RequireFresh: true, MaxSourceAge: "48h"}}}
	valid := iplist.Result{Snapshot: strings.Repeat("a", 64), SourceStatus: "ok", SourceUpdatedAt: time.Now()}
	if err := r.validateFreshness(config.SourceConfig{}, valid); err != nil {
		t.Fatal(err)
	}
	for _, result := range []iplist.Result{{}, {Snapshot: valid.Snapshot, SourceStatus: "degraded", SourceUpdatedAt: time.Now()}, {Snapshot: valid.Snapshot, SourceStatus: "ok", SourceUpdatedAt: time.Now().Add(-72 * time.Hour)}, {Snapshot: valid.Snapshot, SourceStatus: "ok", SourceUpdatedAt: time.Now().Add(time.Hour)}} {
		if err := r.validateFreshness(config.SourceConfig{}, result); err == nil {
			t.Errorf("accepted invalid freshness: %+v", result)
		}
	}
}
