package iplist

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strings"
)

const maxSourceBytes = 16 << 20

type Fetcher struct {
	Client *http.Client
}

type Result struct {
	Values []string
	Hash   string
}

func (f Fetcher) Fetch(ctx context.Context, sourceURL, dataType string) (Result, error) {
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Result{}, fmt.Errorf("source returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(body) > maxSourceBytes {
		return Result{}, fmt.Errorf("source response exceeds %d bytes", maxSourceBytes)
	}
	values, err := NormalizeLines(string(body), dataType)
	if err != nil {
		return Result{}, err
	}
	return Result{Values: values, Hash: HashValues(values)}, nil
}

func NormalizeLines(body, dataType string) ([]string, error) {
	seen := map[string]struct{}{}
	var values []string
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		value, err := normalizeValue(line, dataType)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", line, err)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Strings(values)
	return values, nil
}

func normalizeValue(value, dataType string) (string, error) {
	switch dataType {
	case "ipv4_cidr", "ipv6_cidr", "ip_cidr":
		prefix, err := parsePrefixOrAddr(value)
		if err != nil {
			return "", err
		}
		if dataType == "ipv4_cidr" && !prefix.Addr().Is4() {
			return "", fmt.Errorf("expected IPv4 CIDR")
		}
		if dataType == "ipv6_cidr" && !prefix.Addr().Is6() {
			return "", fmt.Errorf("expected IPv6 CIDR")
		}
		return prefix.Masked().String(), nil
	case "domains":
		domain := strings.TrimSuffix(strings.ToLower(value), ".")
		if domain == "" || strings.ContainsAny(domain, " /") {
			return "", fmt.Errorf("invalid domain")
		}
		return domain, nil
	default:
		return "", fmt.Errorf("unsupported data type %q", dataType)
	}
}

func parsePrefixOrAddr(value string) (netip.Prefix, error) {
	if strings.Contains(value, "/") {
		return netip.ParsePrefix(value)
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	if addr.Is4() {
		return netip.PrefixFrom(addr, 32), nil
	}
	return netip.PrefixFrom(addr, 128), nil
}

func HashValues(values []string) string {
	h := sha256.New()
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
