// Package network defines interfaces for network operations.
// This allows sandboxing of HTTP requests and other network activity.
package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
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
//
// A client may either buffer the payload in Body or stream it through
// BodyReader. When BodyReader is set it is the body: fetch resolves as soon
// as Do returns and pulls chunks from the reader. Body is the buffered
// fallback used when BodyReader is nil. Callers that want the full payload
// regardless of which form the client used should call Bytes, which consumes
// and closes BodyReader.
type Response struct {
	StatusCode    int
	Status        string
	Headers       map[string][]string
	Body          []byte
	ContentLength int64
	// BodyReader is the streaming response body. The caller must Close it,
	// unless Bytes is used, which closes it. Nil when the client buffered
	// the payload in Body instead.
	BodyReader io.ReadCloser
}

// Bytes returns the full response payload. A buffered Body is returned as-is.
// A BodyReader is read to completion and closed, and the bytes are also
// stored on Body.
func (r *Response) Bytes() ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if r.BodyReader == nil {
		return r.Body, nil
	}
	rc := r.BodyReader
	r.BodyReader = nil
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	r.Body = body
	return body, nil
}

// HTTPClient defines the interface for making HTTP requests.
type HTTPClient interface {
	// Do performs an HTTP request and returns once response headers are
	// available. Implementations may stream the payload by setting
	// Response.BodyReader; cancelling ctx must abort the exchange and
	// unblock reads of that reader. Buffered clients leave BodyReader nil
	// and put the payload in Body.
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

// NewRealHTTPClient creates a client that streams response bodies.
//
// The 30s client timeout bounds only the wait for response headers, matching
// fetch resolving once headers arrive. Reading BodyReader continues until the
// server finishes or ctx is cancelled. Request.Timeout, when set, bounds the
// whole exchange including the body. Get and Post still buffer the payload and
// apply the client timeout to that full read.
func NewRealHTTPClient() *RealHTTPClient {
	return NewRealHTTPClientWithTimeout(30 * time.Second)
}

// NewRealHTTPClientWithTimeout creates a streaming client whose header timeout
// is timeout. See NewRealHTTPClient for how the timeout applies.
func NewRealHTTPClientWithTimeout(timeout time.Duration) *RealHTTPClient {
	return &RealHTTPClient{
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Do performs an HTTP request and returns when response headers arrive.
// The body is streamed through Response.BodyReader; the caller must Close it
// (Bytes does this). Cancelling ctx aborts the request and unblocks body reads.
func (c *RealHTTPClient) Do(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var bodyReader io.Reader
	if req.Body != nil {
		bodyReader = bytes.NewReader(req.Body)
	}

	// Request.Timeout covers headers and the body. The client timeout covers
	// headers only, so a long-lived stream is not cut off after Do returns.
	callCtx := ctx
	var timeoutCancel context.CancelFunc
	if req.Timeout > 0 {
		callCtx, timeoutCancel = context.WithTimeout(ctx, req.Timeout)
	}

	httpReq, err := http.NewRequestWithContext(callCtx, req.Method, req.URL, bodyReader)
	if err != nil {
		if timeoutCancel != nil {
			timeoutCancel()
		}
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

	// Timeout is applied to the request context above (overall) or as a headers
	// timer below. The http.Client timeout must stay 0 so it does not also
	// cancel body reads after Do returns.
	client := &http.Client{
		Transport:     c.client.Transport,
		CheckRedirect: c.client.CheckRedirect,
		Jar:           c.client.Jar,
	}
	if req.NoFollowRedirects {
		// Return the 3xx response as-is instead of following it, matching
		// Node's http.request semantics.
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	headerTimeout := time.Duration(0)
	if req.Timeout == 0 {
		headerTimeout = c.client.Timeout
	}

	if headerTimeout > 0 {
		headerCtx, headerCancel := context.WithCancel(callCtx)
		timer := time.AfterFunc(headerTimeout, headerCancel)
		httpReq = httpReq.WithContext(headerCtx)
		resp, err := client.Do(httpReq)
		if !timer.Stop() {
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			headerCancel()
			return nil, fmt.Errorf("request timed out after %s awaiting headers: %w", headerTimeout, context.DeadlineExceeded)
		}
		if err != nil {
			headerCancel()
			return nil, err
		}
		resp.Body = &onCloseReader{ReadCloser: resp.Body, fn: headerCancel}
		return responseFromHTTP(resp), nil
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		if timeoutCancel != nil {
			timeoutCancel()
		}
		return nil, err
	}
	if timeoutCancel != nil {
		resp.Body = &onCloseReader{ReadCloser: resp.Body, fn: timeoutCancel}
	}
	return responseFromHTTP(resp), nil
}

// Get performs a GET request and buffers the response body.
func (c *RealHTTPClient) Get(ctx context.Context, url string) (*Response, error) {
	return c.doBuffered(ctx, &Request{
		Method: "GET",
		URL:    url,
	})
}

// Post performs a POST request and buffers the response body.
func (c *RealHTTPClient) Post(ctx context.Context, url string, contentType string, body []byte) (*Response, error) {
	return c.doBuffered(ctx, &Request{
		Method: "POST",
		URL:    url,
		Headers: map[string][]string{
			"content-type": {contentType},
		},
		Body: body,
	})
}

// doBuffered runs Do and reads the streamed body to completion. A client
// timeout that Do would apply only to headers is widened to the whole read,
// preserving the previous Get/Post deadline.
func (c *RealHTTPClient) doBuffered(ctx context.Context, req *Request) (*Response, error) {
	if req.Timeout == 0 && c.client != nil && c.client.Timeout > 0 {
		req.Timeout = c.client.Timeout
	}
	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	if _, err := resp.Bytes(); err != nil {
		return nil, err
	}
	return resp, nil
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
		MaxRequestSize:    10 * 1024 * 1024, // 10MB
		MaxResponseSize:   50 * 1024 * 1024, // 50MB
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

		if c.MaxResponseSize > 0 {
			if resp.BodyReader != nil {
				resp.BodyReader = &maxBytesReadCloser{rc: resp.BodyReader, left: c.MaxResponseSize}
			} else if int64(len(resp.Body)) > c.MaxResponseSize {
				return nil, errResponseTooLarge
			}
		}

		return resp, nil
	}

	return nil, ErrRequestBlocked
}

// Get performs a sandboxed GET request and buffers the body.
func (c *SandboxedHTTPClient) Get(ctx context.Context, url string) (*Response, error) {
	return bufferResponse(c.Do(ctx, &Request{
		Method: "GET",
		URL:    url,
	}))
}

// Post performs a sandboxed POST request and buffers the body.
func (c *SandboxedHTTPClient) Post(ctx context.Context, url string, contentType string, body []byte) (*Response, error) {
	return bufferResponse(c.Do(ctx, &Request{
		Method: "POST",
		URL:    url,
		Headers: map[string][]string{
			"content-type": {contentType},
		},
		Body: body,
	}))
}

func bufferResponse(resp *Response, err error) (*Response, error) {
	if err != nil {
		return nil, err
	}
	if _, err := resp.Bytes(); err != nil {
		return nil, err
	}
	return resp, nil
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

// errResponseTooLarge is returned when a sandboxed response exceeds MaxResponseSize.
var errResponseTooLarge = errors.New("EMSGSIZE: response body too large")

// responseFromHTTP copies headers and leaves the body as a stream.
func responseFromHTTP(resp *http.Response) *Response {
	headers := make(map[string][]string, len(resp.Header))
	for key, values := range resp.Header {
		headers[strings.ToLower(key)] = append([]string(nil), values...)
	}
	return &Response{
		StatusCode:    resp.StatusCode,
		Status:        resp.Status,
		Headers:       headers,
		BodyReader:    resp.Body,
		ContentLength: resp.ContentLength,
	}
}

// onCloseReader runs fn exactly once when the body is closed. It releases the
// header-timeout or overall-timeout context that body reads are bound to.
type onCloseReader struct {
	io.ReadCloser
	once sync.Once
	fn   func()
}

func (r *onCloseReader) Close() error {
	err := r.ReadCloser.Close()
	if r.fn != nil {
		r.once.Do(r.fn)
	}
	return err
}

// maxBytesReadCloser fails the read after limit bytes instead of buffering the
// whole payload to measure it. A body of exactly limit bytes is allowed.
type maxBytesReadCloser struct {
	rc   io.ReadCloser
	left int64
	err  error
}

func (m *maxBytesReadCloser) Read(p []byte) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	if m.left <= 0 {
		m.err = errResponseTooLarge
		return 0, m.err
	}
	if int64(len(p)) > m.left {
		p = p[:m.left]
	}
	n, err := m.rc.Read(p)
	if n > 0 {
		m.left -= int64(n)
	}
	if err != nil {
		return n, err
	}
	if m.left == 0 {
		var extra [1]byte
		nn, peekErr := m.rc.Read(extra[:])
		if nn > 0 {
			// The bytes already copied into p are within the limit. The next
			// read reports the overflow so callers can finish this chunk first.
			m.err = errResponseTooLarge
			return n, nil
		}
		if peekErr != nil && !errors.Is(peekErr, io.EOF) {
			m.err = peekErr
			return n, nil
		}
		m.err = io.EOF
	}
	return n, nil
}

func (m *maxBytesReadCloser) Close() error {
	return m.rc.Close()
}
