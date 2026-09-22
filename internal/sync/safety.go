package sync

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/dexogen/iplist-go-unifi/internal/config"
	"github.com/dexogen/iplist-go-unifi/internal/iplist"
	"github.com/resnickio/unifi-go-sdk/pkg/unifi"
)

func (r *Reconciler) validateFreshness(source config.SourceConfig, result iplist.Result) error {
	if source.RequiresFresh(r.Config.Safety.RequireFresh) {
		digest, err := hex.DecodeString(result.Snapshot)
		if err != nil || len(digest) != 32 || result.SourceUpdatedAt.IsZero() || result.SourceStatus == "" {
			return fmt.Errorf("source has no valid snapshot/freshness metadata")
		}
	}
	if result.SourceStatus != "" && result.SourceStatus != "ok" {
		return fmt.Errorf("source status is %s; retaining current route", result.SourceStatus)
	}
	if !result.SourceUpdatedAt.IsZero() {
		maxAge := 48 * time.Hour
		if r.Config.Safety.MaxSourceAge != "" {
			var err error
			maxAge, err = time.ParseDuration(r.Config.Safety.MaxSourceAge)
			if err != nil || maxAge <= 0 {
				return fmt.Errorf("invalid max_source_age")
			}
		}
		age := time.Since(result.SourceUpdatedAt)
		if age > maxAge || age < -10*time.Minute {
			return fmt.Errorf("source timestamp is outside the allowed freshness window")
		}
	}
	return nil
}

type prefixTree struct {
	full     bool
	children [2]*prefixTree
}

func addressBit(address netip.Addr, bit int) byte {
	if address.Is4() {
		bytes := address.As4()
		return (bytes[bit/8] >> uint(7-bit%8)) & 1
	}
	bytes := address.As16()
	return (bytes[bit/8] >> uint(7-bit%8)) & 1
}

func (t *prefixTree) insert(prefix netip.Prefix, depth int) {
	if t.full {
		return
	}
	if depth == prefix.Bits() {
		t.full = true
		t.children = [2]*prefixTree{}
		return
	}
	bit := addressBit(prefix.Addr(), depth)
	if t.children[bit] == nil {
		t.children[bit] = &prefixTree{}
	}
	t.children[bit].insert(prefix, depth+1)
	if t.children[0] != nil && t.children[1] != nil && t.children[0].full && t.children[1].full {
		t.full = true
		t.children = [2]*prefixTree{}
	}
}

func (t *prefixTree) covers(prefix netip.Prefix, depth int) bool {
	if t == nil {
		return false
	}
	if t.full {
		return true
	}
	if depth == prefix.Bits() {
		return false
	}
	return t.children[addressBit(prefix.Addr(), depth)].covers(prefix, depth+1)
}

func routePrefix(value string) (netip.Prefix, error) {
	if strings.Contains(value, "/") {
		p, err := netip.ParsePrefix(value)
		return p.Masked(), err
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func uncoveredRoutes(current unifi.TrafficRoute, desired *unifi.TrafficRoute) int {
	missing := 0
	if desired.MatchingTarget == "DOMAIN" {
		for _, old := range current.Domains {
			covered := false
			for _, next := range desired.Domains {
				a, b := strings.ToLower(old.Domain), strings.ToLower(next.Domain)
				if a == b || strings.HasSuffix(a, "."+b) {
					covered = true
					break
				}
			}
			if !covered {
				missing++
			}
		}
		return missing
	}
	trees := map[bool]*prefixTree{true: {}, false: {}}
	for _, value := range desired.IPAddresses {
		if prefix, err := routePrefix(value); err == nil {
			trees[prefix.Addr().Is4()].insert(prefix, 0)
		}
	}
	for _, value := range current.IPAddresses {
		prefix, err := routePrefix(value)
		if err != nil || !trees[prefix.Addr().Is4()].covers(prefix, 0) {
			missing++
		}
	}
	return missing
}
