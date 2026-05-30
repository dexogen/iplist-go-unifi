package unificlient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/resnickio/unifi-go-sdk/pkg/unifi"
)

const maxBody = 32 << 20

type Config struct {
	BaseURL            string
	APIKey             string
	Username           string
	Password           string
	Site               string
	InsecureSkipVerify bool
	Timeout            time.Duration
}

type Client interface {
	Login(ctx context.Context) error
	Logout(ctx context.Context) error
	ListNetworks(ctx context.Context) ([]unifi.Network, error)
	ListTrafficRoutes(ctx context.Context) ([]unifi.TrafficRoute, error)
	CreateTrafficRoute(ctx context.Context, route *unifi.TrafficRoute) (*unifi.TrafficRoute, error)
	UpdateTrafficRoute(ctx context.Context, id string, route *unifi.TrafficRoute) (*unifi.TrafficRoute, error)
}

type RawClient struct {
	baseURL    string
	apiKey     string
	username   string
	password   string
	site       string
	httpClient *http.Client
	csrfToken  string
}

type legacyResponse struct {
	Meta struct {
		RC  string `json:"rc"`
		Msg string `json:"msg"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

type trafficRouteWire struct {
	ID                string                    `json:"_id,omitempty"`
	Name              string                    `json:"name,omitempty"`
	Enabled           *bool                     `json:"enabled,omitempty"`
	Description       string                    `json:"description,omitempty"`
	MatchingTarget    string                    `json:"matching_target,omitempty"`
	TargetDevices     []unifi.TrafficRuleTarget `json:"target_devices,omitempty"`
	NetworkID         string                    `json:"network_id,omitempty"`
	Domains           []unifi.TrafficDomain     `json:"domains,omitempty"`
	IPAddresses       []ipAddressWire           `json:"ip_addresses,omitempty"`
	IPRanges          []string                  `json:"ip_ranges,omitempty"`
	Regions           []string                  `json:"regions,omitempty"`
	KillSwitchEnabled *bool                     `json:"kill_switch_enabled,omitempty"`
}

type ipAddressWire struct {
	IPOrSubnet string `json:"ip_or_subnet"`
	IPVersion  string `json:"ip_version,omitempty"`
	Ports      []int  `json:"ports"`
	PortRanges []any  `json:"port_ranges"`
}

func New(cfg Config) (Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if cfg.Site == "" {
		cfg.Site = "default"
	}
	hasAPIKey := cfg.APIKey != ""
	hasCredentials := cfg.Username != "" || cfg.Password != ""
	if hasAPIKey && hasCredentials {
		return nil, fmt.Errorf("use either api key or username/password, not both")
	}
	if !hasAPIKey && (cfg.Username == "" || cfg.Password == "") {
		return nil, fmt.Errorf("api key or username/password is required")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	return &RawClient{
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:   cfg.APIKey,
		username: cfg.Username,
		password: cfg.Password,
		site:     cfg.Site,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			Jar:       jar,
		},
	}, nil
}

func (c *RawClient) Login(ctx context.Context) error {
	if c.apiKey != "" {
		return nil
	}
	payload := map[string]string{"username": c.username, "password": c.password}
	if _, err := c.do(ctx, http.MethodPost, "/api/auth/login", payload); err != nil {
		return err
	}
	resp, err := c.request(ctx, http.MethodGet, c.legacyPath("self"), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	c.csrfToken = resp.Header.Get("X-Csrf-Token")
	return nil
}

func (c *RawClient) Logout(ctx context.Context) error {
	if c.apiKey != "" {
		return nil
	}
	_, err := c.do(ctx, http.MethodPost, "/api/auth/logout", nil)
	return err
}

func (c *RawClient) ListNetworks(ctx context.Context) ([]unifi.Network, error) {
	body, err := c.do(ctx, http.MethodGet, c.restPath("networkconf"), nil)
	if err != nil {
		return nil, err
	}
	var wrapper legacyResponse
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, err
	}
	if wrapper.Meta.RC != "ok" {
		return nil, fmt.Errorf("unifi api error: %s", wrapper.Meta.Msg)
	}
	var networks []unifi.Network
	if err := json.Unmarshal(wrapper.Data, &networks); err != nil {
		return nil, err
	}
	return networks, nil
}

func (c *RawClient) ListTrafficRoutes(ctx context.Context) ([]unifi.TrafficRoute, error) {
	body, err := c.do(ctx, http.MethodGet, c.v2Path("trafficroutes"), nil)
	if err != nil {
		return nil, err
	}
	var wire []trafficRouteWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	routes := make([]unifi.TrafficRoute, 0, len(wire))
	for _, item := range wire {
		routes = append(routes, item.toSDK())
	}
	return routes, nil
}

func (c *RawClient) CreateTrafficRoute(ctx context.Context, route *unifi.TrafficRoute) (*unifi.TrafficRoute, error) {
	body, err := c.do(ctx, http.MethodPost, c.v2Path("trafficroutes"), fromSDK(route))
	if err != nil {
		return nil, err
	}
	return decodeTrafficRoute(body, route.Name)
}

func (c *RawClient) UpdateTrafficRoute(ctx context.Context, id string, route *unifi.TrafficRoute) (*unifi.TrafficRoute, error) {
	body, err := c.do(ctx, http.MethodPut, c.v2Path("trafficroutes")+"/"+url.PathEscape(id), fromSDK(route))
	if err != nil {
		return nil, err
	}
	return decodeTrafficRoute(body, route.Name)
}

func decodeTrafficRoute(body []byte, fallbackName string) (*unifi.TrafficRoute, error) {
	var wire trafficRouteWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	route := wire.toSDK()
	if route.Name == "" {
		route.Name = fallbackName
	}
	return &route, nil
}

func (w trafficRouteWire) toSDK() unifi.TrafficRoute {
	route := unifi.TrafficRoute{
		ID:                w.ID,
		Name:              w.Name,
		Enabled:           w.Enabled,
		Description:       w.Description,
		MatchingTarget:    w.MatchingTarget,
		TargetDevices:     w.TargetDevices,
		NetworkID:         w.NetworkID,
		Domains:           w.Domains,
		IPRanges:          w.IPRanges,
		Regions:           w.Regions,
		KillSwitchEnabled: w.KillSwitchEnabled,
	}
	for _, item := range w.IPAddresses {
		if item.IPOrSubnet != "" {
			route.IPAddresses = append(route.IPAddresses, item.IPOrSubnet)
		}
	}
	return route
}

func fromSDK(route *unifi.TrafficRoute) trafficRouteWire {
	wire := trafficRouteWire{
		ID:                route.ID,
		Name:              route.Name,
		Enabled:           route.Enabled,
		Description:       route.Description,
		MatchingTarget:    route.MatchingTarget,
		TargetDevices:     route.TargetDevices,
		NetworkID:         route.NetworkID,
		Domains:           route.Domains,
		IPRanges:          route.IPRanges,
		Regions:           route.Regions,
		KillSwitchEnabled: route.KillSwitchEnabled,
	}
	for _, value := range route.IPAddresses {
		wire.IPAddresses = append(wire.IPAddresses, ipAddressWire{
			IPOrSubnet: value,
			IPVersion:  ipVersion(value),
			Ports:      []int{},
			PortRanges: []any{},
		})
	}
	return wire
}

func ipVersion(value string) string {
	if strings.Contains(value, "/") {
		if prefix, err := netip.ParsePrefix(value); err == nil {
			if prefix.Addr().Is4() {
				return "v4"
			}
			return "v6"
		}
	}
	if addr, err := netip.ParseAddr(value); err == nil && addr.Is4() {
		return "v4"
	}
	return "v6"
}

func (c *RawClient) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	resp, err := c.request(ctx, method, path, payload)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	limit := int64(maxBody)
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s returned %s: %s", method, path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *RawClient) request(ctx context.Context, method, path string, payload any) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-KEY", c.apiKey)
	}
	if c.csrfToken != "" && (method == http.MethodPost || method == http.MethodPut || method == http.MethodDelete) {
		req.Header.Set("X-Csrf-Token", c.csrfToken)
	}
	return c.httpClient.Do(req)
}

func (c *RawClient) legacyPath(endpoint string) string {
	return "/proxy/network/api/s/" + url.PathEscape(c.site) + "/" + endpoint
}

func (c *RawClient) restPath(endpoint string) string {
	return "/proxy/network/api/s/" + url.PathEscape(c.site) + "/rest/" + endpoint
}

func (c *RawClient) v2Path(endpoint string) string {
	return "/proxy/network/v2/api/site/" + url.PathEscape(c.site) + "/" + endpoint
}
