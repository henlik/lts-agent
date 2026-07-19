// Package core provides the secure HTTPS/JSON transport foundation for LTS
// Core. Version 0.5 defines transport behavior only; registration,
// authentication, and heartbeats are intentionally implemented later.
package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// MaxResponseBytes bounds memory consumed by any Core response.
	MaxResponseBytes = 1024 * 1024
	// MaxErrorExcerptBytes keeps HTTP status errors useful without embedding an
	// entire response in logs or higher-level error messages.
	MaxErrorExcerptBytes = 4 * 1024
	MaxBearerBytes       = 8 * 1024
)

// ErrResponseTooLarge identifies a response that exceeded MaxResponseBytes.
var ErrResponseTooLarge = errors.New("LTS Core response exceeds 1 MiB limit")

// Options configures a Client. These values remain code-level in v0.5 and will
// be connected to node configuration when registration is introduced.
type Options struct {
	BaseURL   string
	Timeout   time.Duration
	CAFile    string
	UserAgent string
}

// HTTPError represents a valid HTTP response outside the 2xx range.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("LTS Core returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("LTS Core returned HTTP %d: %s", e.StatusCode, e.Body)
}

// Client sends bounded JSON requests to one trusted LTS Core origin.
type Client struct {
	baseURL   *url.URL
	http      *http.Client
	userAgent string
}

// New validates all transport options and constructs a client. It performs no
// network request.
func New(options Options) (*Client, error) {
	baseURL, err := parseBaseURL(options.BaseURL)
	if err != nil {
		return nil, err
	}
	if options.Timeout <= 0 {
		return nil, fmt.Errorf("timeout must be greater than zero")
	}
	if strings.TrimSpace(options.UserAgent) == "" {
		return nil, fmt.Errorf("user agent must not be empty")
	}
	if strings.ContainsAny(options.UserAgent, "\r\n") {
		return nil, fmt.Errorf("user agent must not contain line breaks")
	}

	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("load system certificate pool: %w", err)
	}
	if rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	if options.CAFile != "" {
		certificate, err := os.ReadFile(options.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %s: %w", options.CAFile, err)
		}
		if ok := rootCAs.AppendCertsFromPEM(certificate); !ok {
			return nil, fmt.Errorf("CA file %s contains no valid PEM certificates", options.CAFile)
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}

	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Transport: transport,
			Timeout:   options.Timeout,
			// Returning the first redirect response prevents future credentials
			// from being forwarded to another endpoint or origin.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		userAgent: options.UserAgent,
	}, nil
}

// DoJSON performs one typed JSON exchange. requestBody may be nil for methods
// without a body, and responseTarget may be nil when the caller only needs the
// success status.
func (c *Client) DoJSON(
	ctx context.Context,
	method string,
	relativeEndpoint string,
	requestBody any,
	responseTarget any,
) error {
	return c.doJSON(ctx, method, relativeEndpoint, "", requestBody, responseTarget)
}

// DoJSONWithBearer performs a JSON exchange authenticated with a bearer token.
// The token is validated before request construction and is never included in
// returned errors.
func (c *Client) DoJSONWithBearer(
	ctx context.Context,
	method string,
	relativeEndpoint string,
	bearer string,
	requestBody any,
	responseTarget any,
) error {
	if err := ValidateBearer(bearer); err != nil {
		return err
	}
	return c.doJSON(ctx, method, relativeEndpoint, bearer, requestBody, responseTarget)
}

func (c *Client) doJSON(
	ctx context.Context,
	method string,
	relativeEndpoint string,
	bearer string,
	requestBody any,
	responseTarget any,
) error {
	endpoint, err := c.resolveEndpoint(relativeEndpoint)
	if err != nil {
		return err
	}

	var body io.Reader
	if requestBody != nil {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(requestBody); err != nil {
			return fmt.Errorf("encode Core request JSON: %w", err)
		}
		body = &encoded
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("create Core request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("perform Core request: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := readBounded(response.Body)
	if err != nil {
		return fmt.Errorf("read Core response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return &HTTPError{
			StatusCode: response.StatusCode,
			Body:       errorExcerpt(responseBody),
		}
	}
	if responseTarget == nil {
		return nil
	}
	if err := decodeResponse(responseBody, responseTarget); err != nil {
		return fmt.Errorf("decode Core response JSON: %w", err)
	}
	return nil
}

// ValidateBearer accepts bounded visible-ASCII bearer values without spaces or
// control characters. This covers opaque and JWT-style tokens while preventing
// header injection.
func ValidateBearer(token string) error {
	if token == "" {
		return fmt.Errorf("bearer token must not be empty")
	}
	if len(token) > MaxBearerBytes {
		return fmt.Errorf("bearer token exceeds %d-byte limit", MaxBearerBytes)
	}
	for _, value := range []byte(token) {
		if value < 0x21 || value > 0x7e {
			return fmt.Errorf("bearer token contains invalid characters")
		}
	}
	return nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Core base URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("Core base URL must use https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("Core base URL must include a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("Core base URL must not include credentials")
	}
	if parsed.RawQuery != "" {
		return nil, fmt.Errorf("Core base URL must not include a query")
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("Core base URL must not include a fragment")
	}
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	return parsed, nil
}

func (c *Client) resolveEndpoint(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("Core endpoint must not be empty")
	}
	if strings.HasPrefix(raw, "/") {
		return nil, fmt.Errorf("Core endpoint must be relative and must not begin with a slash")
	}

	reference, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Core endpoint: %w", err)
	}
	if reference.IsAbs() || reference.Host != "" || reference.User != nil {
		return nil, fmt.Errorf("Core endpoint must not override the configured origin")
	}
	if reference.Fragment != "" {
		return nil, fmt.Errorf("Core endpoint must not include a fragment")
	}
	for _, segment := range strings.Split(reference.Path, "/") {
		if segment == ".." {
			return nil, fmt.Errorf("Core endpoint must not contain path traversal")
		}
	}

	resolved := c.baseURL.ResolveReference(reference)
	if resolved.Scheme != c.baseURL.Scheme || resolved.Host != c.baseURL.Host {
		return nil, fmt.Errorf("Core endpoint resolved outside the configured origin")
	}
	return resolved, nil
}

func readBounded(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return data, nil
}

func decodeResponse(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("invalid trailing content: %w", err)
	}
	return nil
}

func errorExcerpt(data []byte) string {
	if len(data) > MaxErrorExcerptBytes {
		data = data[:MaxErrorExcerptBytes]
	}
	return strings.TrimSpace(strings.ToValidUTF8(string(data), "�"))
}
