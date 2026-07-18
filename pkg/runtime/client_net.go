// Package network defines interfaces for network operations.
// This allows sandboxing of HTTP requests and other network activity.
package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrRequestBlocked is returned when a request is blocked by sandbox rules
	ErrRequestBlocked = errors.New("ENETUNREACH: request blocked by sandbox")
	// ErrHostNotAllowed is returned when a host is not in the allowlist
	ErrHostNotAllowed = errors.New("ENOTFOUND: host not allowed")
)

// Request represents an HTTP request.
type Request struct {
	Method  string
	URL     string
	Headers map[string][]string
	Body    []byte
	Timeout time.Duration
	// NoFollowRedirects, when true, makes the client return 3xx responses
	// directly instead of following the Location header. Node's low-level
	// http.request/http.ClientRequest never follows redirects (callers such as
	// superagent/axios implement that themselves), so the http module sets this;
	// fetch leaves it false to keep Node's redirect-following fetch semantics.
	NoFollowRedirects bool
}

// Response represents an HTTP response.
type Response struct {
	StatusCode    int
	Status        string
	Headers       map[string][]string
	Body          []byte
	ContentLength int64
}

// HTTPClient defines the interface for making HTTP requests.
type HTTPClient interface {
	// Do performs an HTTP request and returns the response.
	Do(ctx context.Context, req *Request) (*Response, error)

	// Get is a convenience method for GET requests.
	Get(ctx context.Context, url string) (*Response, error)

	// Post is a convenience method for POST requests.
	Post(ctx context.Context, url string, contentType string, body []byte) (*Response, error)
}

// RealHTTPClient implements HTTPClient using Go's net/http.
type RealHTTPClient struct {
	client *http.Client
}

// NewRealHTTPClient creates a new real HTTP client.
func NewRealHTTPClient() *RealHTTPClient {
	return &RealHTTPClient{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewRealHTTPClientWithTimeout creates a client with custom timeout.
func NewRealHTTPClientWithTimeout(timeout time.Duration) *RealHTTPClient {
	return &RealHTTPClient{
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Do performs an HTTP request and returns the response.
func (c *RealHTTPClient) Do(ctx context.Context, req *Request) (*Response, error) {
	var bodyReader io.Reader
	if req.Body != nil {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, err
	}

	for key, values := range req.Headers {
		// Go ignores a "Host" entry in the header map; the Host header on the
		// wire is taken from httpReq.Host (defaulting to the URL authority). Node
		// clients let callers override Host explicitly (superagent/supertest set
		// it to test req.host/hostname handling), so mirror that by copying it
		// onto httpReq.Host instead of dropping it into the header map.
		if strings.EqualFold(key, "host") {
			if len(values) > 0 {
				httpReq.Host = values[0]
			}
			continue
		}
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}

	// Use request-specific timeout and/or redirect policy if needed. Both are
	// per-http.Client settings, so build a lightweight client that reuses the
	// shared Transport (keeping connection pooling) when either differs from the
	// default.
	client := c.client
	if req.Timeout > 0 || req.NoFollowRedirects {
		custom := &http.Client{
			Timeout:   c.client.Timeout,
			Transport: c.client.Transport,
		}
		if req.Timeout > 0 {
			custom.Timeout = req.Timeout
		}
		if req.NoFollowRedirects {
			// Return the 3xx response as-is instead of following it, matching
			// Node's http.request semantics.
			custom.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
		}
		client = custom
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	headers := make(map[string][]string)
	for key, values := range resp.Header {
		headers[strings.ToLower(key)] = values
	}

	return &Response{
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		Headers:       headers,
		Body:          body,
		ContentLength: resp.ContentLength,
	}, nil
}

// Get performs a GET request to the specified URL.
func (c *RealHTTPClient) Get(ctx context.Context, url string) (*Response, error) {
	return c.Do(ctx, &Request{
		Method: "GET",
		URL:    url,
	})
}

// Post performs a POST request with the specified content type and body.
func (c *RealHTTPClient) Post(ctx context.Context, url string, contentType string, body []byte) (*Response, error) {
	return c.Do(ctx, &Request{
		Method: "POST",
		URL:    url,
		Headers: map[string][]string{
			"content-type": {contentType},
		},
		Body: body,
	})
}

// SandboxedHTTPClient implements HTTPClient with restrictions.
type SandboxedHTTPClient struct {
	// AllowedHosts is a list of allowed hostnames (supports wildcards like *.example.com)
	AllowedHosts []string
	// BlockedHosts is a list of blocked hostnames (takes precedence over AllowedHosts)
	BlockedHosts []string
	// AllowedSchemes is a list of allowed URL schemes (default: http, https)
	AllowedSchemes []string
	// MaxRequestSize is the maximum request body size (0 = unlimited)
	MaxRequestSize int64
	// MaxResponseSize is the maximum response body size (0 = unlimited)
	MaxResponseSize int64
	// MockResponses maps URL patterns to mock responses
	MockResponses map[string]*Response
	// DefaultResponse is returned when no mock matches and real requests are blocked
	DefaultResponse *Response
	// AllowRealRequests allows requests to pass through to AllowedHosts
	AllowRealRequests bool
	// underlying client for allowed real requests
	realClient *RealHTTPClient
}

// SandboxHTTPConfig configures the sandboxed HTTP client.
type SandboxHTTPConfig struct {
	AllowedHosts      []string
	BlockedHosts      []string
	AllowedSchemes    []string
	MaxRequestSize    int64
	MaxResponseSize   int64
	AllowRealRequests bool
}

// DefaultSandboxHTTPConfig returns a restrictive default configuration.
func DefaultSandboxHTTPConfig() *SandboxHTTPConfig {
	return &SandboxHTTPConfig{
		AllowedHosts:      []string{}, // No hosts allowed by default
		BlockedHosts:      []string{"localhost", "127.0.0.1", "::1", "0.0.0.0", "169.254.*", "10.*", "172.16.*", "192.168.*"},
		AllowedSchemes:    []string{"http", "https"},
		MaxRequestSize:    10 * 1024 * 1024,  // 10MB
		MaxResponseSize:   50 * 1024 * 1024,  // 50MB
		AllowRealRequests: false,
	}
}

// NewSandboxedHTTPClient creates a new sandboxed HTTP client.
func NewSandboxedHTTPClient(cfg *SandboxHTTPConfig) *SandboxedHTTPClient {
	if cfg == nil {
		cfg = DefaultSandboxHTTPConfig()
	}

	schemes := cfg.AllowedSchemes
	if len(schemes) == 0 {
		schemes = []string{"http", "https"}
	}

	return &SandboxedHTTPClient{
		AllowedHosts:      cfg.AllowedHosts,
		BlockedHosts:      cfg.BlockedHosts,
		AllowedSchemes:    schemes,
		MaxRequestSize:    cfg.MaxRequestSize,
		MaxResponseSize:   cfg.MaxResponseSize,
		MockResponses:     make(map[string]*Response),
		AllowRealRequests: cfg.AllowRealRequests,
		realClient:        NewRealHTTPClient(),
	}
}

// AddMockResponse adds a mock response for a URL pattern.
func (c *SandboxedHTTPClient) AddMockResponse(urlPattern string, resp *Response) {
	c.MockResponses[urlPattern] = resp
}

// SetDefaultResponse sets the default response for blocked requests.
func (c *SandboxedHTTPClient) SetDefaultResponse(resp *Response) {
	c.DefaultResponse = resp
}

// isHostAllowed checks if a host is permitted by the sandbox rules.
func (c *SandboxedHTTPClient) isHostAllowed(host string) bool {
	// Check blocked hosts first
	for _, pattern := range c.BlockedHosts {
		if matchHost(pattern, host) {
			return false
		}
	}

	// If no allowed hosts specified and real requests disabled, block all
	if len(c.AllowedHosts) == 0 && !c.AllowRealRequests {
		return false
	}

	// If allowed hosts specified, check against them
	if len(c.AllowedHosts) > 0 {
		for _, pattern := range c.AllowedHosts {
			if matchHost(pattern, host) {
				return true
			}
		}
		return false
	}

	return c.AllowRealRequests
}

// isSchemeAllowed checks if a URL scheme is permitted.
func (c *SandboxedHTTPClient) isSchemeAllowed(scheme string) bool {
	for _, s := range c.AllowedSchemes {
		if strings.EqualFold(s, scheme) {
			return true
		}
	}
	return false
}

// findMockResponse finds a mock response matching the request URL.
func (c *SandboxedHTTPClient) findMockResponse(reqURL string) *Response {
	for pattern, resp := range c.MockResponses {
		if matchURLPattern(pattern, reqURL) {
			return resp
		}
	}
	return nil
}

// Do performs an HTTP request with sandbox restrictions applied.
func (c *SandboxedHTTPClient) Do(ctx context.Context, req *Request) (*Response, error) {
	// Check request size
	if c.MaxRequestSize > 0 && int64(len(req.Body)) > c.MaxRequestSize {
		return nil, errors.New("EMSGSIZE: request body too large")
	}

	// Parse URL
	parsed, err := url.Parse(req.URL)
	if err != nil {
		return nil, err
	}

	// Check scheme
	if !c.isSchemeAllowed(parsed.Scheme) {
		return nil, ErrRequestBlocked
	}

	// Check for mock response first
	if mock := c.findMockResponse(req.URL); mock != nil {
		return mock, nil
	}

	// Check if host is allowed
	host := parsed.Hostname()
	if !c.isHostAllowed(host) {
		if c.DefaultResponse != nil {
			return c.DefaultResponse, nil
		}
		return nil, ErrHostNotAllowed
	}

	// Make real request if allowed
	if c.AllowRealRequests {
		resp, err := c.realClient.Do(ctx, req)
		if err != nil {
			return nil, err
		}

		// Check response size
		if c.MaxResponseSize > 0 && int64(len(resp.Body)) > c.MaxResponseSize {
			return nil, errors.New("EMSGSIZE: response body too large")
		}

		return resp, nil
	}

	return nil, ErrRequestBlocked
}

// Get performs a sandboxed GET request.
func (c *SandboxedHTTPClient) Get(ctx context.Context, url string) (*Response, error) {
	return c.Do(ctx, &Request{
		Method: "GET",
		URL:    url,
	})
}

// Post performs a sandboxed POST request.
func (c *SandboxedHTTPClient) Post(ctx context.Context, url string, contentType string, body []byte) (*Response, error) {
	return c.Do(ctx, &Request{
		Method: "POST",
		URL:    url,
		Headers: map[string][]string{
			"content-type": {contentType},
		},
		Body: body,
	})
}

// matchHost checks if a host matches a pattern (supports * wildcard).
func matchHost(pattern, host string) bool {
	pattern = strings.ToLower(pattern)
	host = strings.ToLower(host)

	if pattern == host {
		return true
	}

	// Handle wildcard patterns
	if strings.Contains(pattern, "*") {
		// Convert glob pattern to regex
		regexPattern := "^" + regexp.QuoteMeta(pattern) + "$"
		regexPattern = strings.ReplaceAll(regexPattern, `\*`, `.*`)
		re, err := regexp.Compile(regexPattern)
		if err != nil {
			return false
		}
		return re.MatchString(host)
	}

	return false
}

// matchURLPattern checks if a URL matches a pattern.
func matchURLPattern(pattern, reqURL string) bool {
	// Exact match
	if pattern == reqURL {
		return true
	}

	// Prefix match with *
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(reqURL, prefix)
	}

	// Regex match
	if strings.HasPrefix(pattern, "~") {
		re, err := regexp.Compile(strings.TrimPrefix(pattern, "~"))
		if err != nil {
			return false
		}
		return re.MatchString(reqURL)
	}

	return false
}

// NoOpHTTPClient blocks all requests (fully sandboxed).
type NoOpHTTPClient struct {
	DefaultResponse *Response
}

// NewNoOpHTTPClient creates a client that blocks all requests.
func NewNoOpHTTPClient() *NoOpHTTPClient {
	return &NoOpHTTPClient{
		DefaultResponse: &Response{
			StatusCode: 503,
			Status:     "503 Service Unavailable",
			Headers:    map[string][]string{"content-type": {"text/plain"}},
			Body:       []byte("Network access is disabled in sandbox mode"),
		},
	}
}

// Do returns the default response or ErrRequestBlocked.
func (c *NoOpHTTPClient) Do(ctx context.Context, req *Request) (*Response, error) {
	if c.DefaultResponse != nil {
		return c.DefaultResponse, nil
	}
	return nil, ErrRequestBlocked
}

// Get returns the default response or ErrRequestBlocked.
func (c *NoOpHTTPClient) Get(ctx context.Context, url string) (*Response, error) {
	return c.Do(ctx, nil)
}

// Post returns the default response or ErrRequestBlocked.
func (c *NoOpHTTPClient) Post(ctx context.Context, url string, contentType string, body []byte) (*Response, error) {
	return c.Do(ctx, nil)
}
