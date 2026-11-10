package framework

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// FetchGuard validates URLs, DNS and each redirect with a pinned-address transport.
// It performs only a bounded HEAD preflight, never downloads/crawls webpage content.
type FetchGuard struct{ Resolver *net.Resolver }

func publicURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, Fail("INVALID_ARGUMENT", "invalid public URL")
	}
	host := u.Hostname()
	if _, err := netip.ParseAddr(host); err != nil && strings.Trim(host, "0123456789.xXabcdefABCDEF") == "" {
		return nil, Fail("PERMISSION_DENIED", "noncanonical IP addresses are forbidden")
	}
	if u.Port() != "" && u.Port() != "80" && u.Port() != "443" {
		return nil, Fail("PERMISSION_DENIED", "fetch port is not allowed")
	}
	return u, nil
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48", "2001:db8::/32"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}
func (g FetchGuard) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	resolver := g.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, Fail("PERMISSION_DENIED", "fetch host cannot be verified")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, Fail("PERMISSION_DENIED", "internal fetch targets are forbidden")
		}
	}
	return ips, nil
}
func (g FetchGuard) Check(ctx context.Context, raw string) error {
	u, err := publicURL(raw)
	if err != nil {
		return err
	}
	_, err = g.resolve(ctx, u.Hostname())
	return err
}
func (g FetchGuard) Preflight(ctx context.Context, raw string) error {
	if err := g.Check(ctx, raw); err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := g.resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: g.checkRedirect}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return Fail("INVALID_ARGUMENT", "invalid fetch URL")
	}
	response, err := client.Do(req)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return e
		}
		return Fail("PROVIDER_ERROR", "fetch target preflight failed")
	}
	response.Body.Close()
	return nil
}

func (g FetchGuard) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return Fail("PERMISSION_DENIED", "fetch redirect limit exceeded")
	}
	return g.Check(req.Context(), req.URL.String())
}

type urlGuard interface {
	Check(context.Context, string) error
	Preflight(context.Context, string) error
}

type DeepSeekConfig struct {
	HTTPConfig
	MaxResults      int
	MaxContentChars int
	Retries         int
	SearchInterval  time.Duration
	FetchInterval   time.Duration
	// RemoteFetchSafe attests the deployment's provider enforces public targets on every
	// redirect and DNS resolution. The official API exposes no redirect-control field.
	// Fetch stays disabled without this explicit server-side guarantee.
	RemoteFetchSafe bool
}
type DeepSeekProvider struct {
	client                *http.Client
	base, key             string
	config                DeepSeekConfig
	guard                 urlGuard
	mu                    sync.Mutex
	nextSearch, nextFetch time.Time
}

func NewDeepSeekProvider(c DeepSeekConfig) (*DeepSeekProvider, error) {
	if c.BaseURL == "" {
		c.BaseURL = "https://api.deepseek.com"
	}
	client, base, err := newHTTP(c.HTTPConfig)
	if err != nil {
		return nil, err
	}
	if c.MaxResults <= 0 || c.MaxResults > 50 {
		c.MaxResults = 10
	}
	if c.MaxContentChars <= 0 {
		c.MaxContentChars = 16000
	}
	if c.Retries < 0 || c.Retries > 3 {
		return nil, Fail("INVALID_ARGUMENT", "retries must be between zero and three")
	}
	if c.SearchInterval <= 0 {
		c.SearchInterval = 100 * time.Millisecond
	}
	if c.FetchInterval <= 0 {
		c.FetchInterval = 200 * time.Millisecond
	}
	return &DeepSeekProvider{client: client, base: base, key: c.APIKey, config: c, guard: FetchGuard{}}, nil
}
func (p *DeepSeekProvider) slot(ctx context.Context, fetch bool) error {
	p.mu.Lock()
	next := &p.nextSearch
	interval := p.config.SearchInterval
	if fetch {
		next = &p.nextFetch
		interval = p.config.FetchInterval
	}
	now := time.Now()
	at := *next
	if at.Before(now) {
		at = now
	}
	*next = at.Add(interval)
	p.mu.Unlock()
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (p *DeepSeekProvider) call(ctx context.Context, fetch bool, path string, input, output any) error {
	for attempt := 0; ; attempt++ {
		if err := p.slot(ctx, fetch); err != nil {
			return err
		}
		err := postJSON(ctx, p.client, p.base+path, p.key, input, output)
		if err == nil {
			return nil
		}
		e := SafeError(err)
		if !e.Retryable || attempt >= p.config.Retries {
			return e
		}
		delay := time.NewTimer(time.Duration(1<<attempt) * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			delay.Stop()
			return ctx.Err()
		case <-delay.C:
		}
	}
}
func clip(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max])
}
func (p *DeepSeekProvider) Search(ctx context.Context, r SearchRequest) ([]SearchResult, error) {
	if strings.TrimSpace(r.Query) == "" || utf8.RuneCountInString(r.Query) > 128 || r.Count < 1 || r.Count > p.config.MaxResults {
		return nil, Fail("INVALID_ARGUMENT", "invalid search query or count")
	}
	var response struct {
		Results *[]SearchResult `json:"results"`
	}
	if err := p.call(ctx, false, "/v1/tools/search", map[string]any{"query": r.Query, "count": r.Count, "need_content": true}, &response); err != nil {
		return nil, err
	}
	if response.Results == nil {
		return nil, Fail("PROVIDER_ERROR", "search results missing")
	}
	results := make([]SearchResult, 0, r.Count)
	for _, result := range *response.Results {
		// Search sources are references, not fetch targets; anchors are valid.
		source, err := url.Parse(result.URL)
		if err != nil {
			continue
		}
		source.Fragment = ""
		if _, err = publicURL(source.String()); err != nil {
			continue
		}
		result.Content = clip(result.Content, p.config.MaxContentChars)
		result.Summary = clip(result.Summary, 2000)
		result.Snippet = clip(result.Snippet, 1000)
		result.Title = clip(result.Title, 512)
		results = append(results, result)
		if len(results) == r.Count {
			break
		}
	}
	if len(*response.Results) > 0 && len(results) == 0 {
		return nil, Fail("PROVIDER_ERROR", "search returned no valid source URLs")
	}

	return results, nil
}
func (p *DeepSeekProvider) Fetch(ctx context.Context, raw string) (Page, error) {
	if err := p.guard.Check(ctx, raw); err != nil {
		return Page{}, err
	}
	if !p.config.RemoteFetchSafe {
		return Page{}, Fail("NOT_IMPLEMENTED", "remote fetch requires a provider guarantee for redirect and DNS target validation")
	}
	if err := p.guard.Preflight(ctx, raw); err != nil {
		return Page{}, err
	}
	var response struct {
		Title   string  `json:"title"`
		Content *string `json:"content"`
	}
	if err := p.call(ctx, true, "/v1/tools/fetch", map[string]string{"url": raw}, &response); err != nil {
		return Page{}, err
	}
	if response.Content == nil || *response.Content == "" {
		return Page{}, Fail("PROVIDER_ERROR", "fetch content missing")
	}
	return Page{URL: raw, Title: clip(response.Title, 512), Content: clip(*response.Content, p.config.MaxContentChars)}, nil
}

var _ SearchProvider = (*DeepSeekProvider)(nil)
