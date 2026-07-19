package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoJSONSendsAndReceivesTypedJSON(t *testing.T) {
	t.Parallel()

	type requestDocument struct {
		Node string `json:"node"`
	}
	type responseDocument struct {
		ID string `json:"id"`
	}

	server := newTLSServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/nodes" || request.URL.RawQuery != "site=lab" {
			t.Errorf("request URL = %s, want /api/v1/nodes?site=lab", request.URL.String())
		}
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.Header.Get("Accept") != "application/json" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("JSON headers = %#v", request.Header)
		}
		if request.Header.Get("User-Agent") != "lts-agent/0.5.0" {
			t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
		}
		var document requestDocument
		if err := json.NewDecoder(request.Body).Decode(&document); err != nil || document.Node != "lts-app-001" {
			t.Errorf("request document = %#v, error = %v", document, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		fmt.Fprint(writer, `{"id":"node-123"}`)
	}))
	client := clientForServer(t, server, "/api/", time.Second)

	var response responseDocument
	err := client.DoJSON(
		context.Background(),
		http.MethodPost,
		"v1/nodes?site=lab",
		requestDocument{Node: "lts-app-001"},
		&response,
	)
	if err != nil || response.ID != "node-123" {
		t.Fatalf("DoJSON() response = %#v, error = %v", response, err)
	}
}

func TestDoJSONWithBearerSetsAuthorizationWithoutLeakingInvalidTokens(t *testing.T) {
	t.Parallel()

	server := newTLSServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer node-token_123" {
			t.Errorf("Authorization = %q", got)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	client := clientForServer(t, server, "/", time.Second)
	if err := client.DoJSONWithBearer(context.Background(), http.MethodPost, "heartbeat", "node-token_123", nil, nil); err != nil {
		t.Fatalf("DoJSONWithBearer() error = %v", err)
	}

	for _, token := range []string{"", "token with space", "token\nsecret", strings.Repeat("x", MaxBearerBytes+1)} {
		err := client.DoJSONWithBearer(context.Background(), http.MethodPost, "heartbeat", token, nil, nil)
		if err == nil {
			t.Fatalf("token %q error = nil", token)
		}
		if token != "" && strings.Contains(err.Error(), token) {
			t.Fatalf("error leaked token: %v", err)
		}
	}
}

func TestNewRejectsInvalidOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options Options
		want    string
	}{
		{name: "malformed URL", options: Options{BaseURL: "://", Timeout: time.Second, UserAgent: "agent"}, want: "base URL"},
		{name: "HTTP URL", options: Options{BaseURL: "http://core.example", Timeout: time.Second, UserAgent: "agent"}, want: "must use https"},
		{name: "missing host", options: Options{BaseURL: "https:///api", Timeout: time.Second, UserAgent: "agent"}, want: "include a host"},
		{name: "credentials", options: Options{BaseURL: "https://user:pass@core.example", Timeout: time.Second, UserAgent: "agent"}, want: "credentials"},
		{name: "query", options: Options{BaseURL: "https://core.example?x=1", Timeout: time.Second, UserAgent: "agent"}, want: "query"},
		{name: "fragment", options: Options{BaseURL: "https://core.example#part", Timeout: time.Second, UserAgent: "agent"}, want: "fragment"},
		{name: "zero timeout", options: Options{BaseURL: "https://core.example", UserAgent: "agent"}, want: "timeout"},
		{name: "negative timeout", options: Options{BaseURL: "https://core.example", Timeout: -time.Second, UserAgent: "agent"}, want: "timeout"},
		{name: "empty user agent", options: Options{BaseURL: "https://core.example", Timeout: time.Second}, want: "user agent"},
		{name: "header injection", options: Options{BaseURL: "https://core.example", Timeout: time.Second, UserAgent: "agent\r\nbad"}, want: "line breaks"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNewRejectsUnreadableAndInvalidCABundles(t *testing.T) {
	t.Parallel()

	base := Options{BaseURL: "https://core.example", Timeout: time.Second, UserAgent: "agent"}
	base.CAFile = filepath.Join(t.TempDir(), "missing.pem")
	if _, err := New(base); err == nil || !strings.Contains(err.Error(), "read CA file") {
		t.Fatalf("New(missing CA) error = %v", err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	base.CAFile = invalidPath
	if _, err := New(base); err == nil || !strings.Contains(err.Error(), "no valid PEM certificates") {
		t.Fatalf("New(invalid CA) error = %v", err)
	}
}

func TestResolveEndpointRejectsUnsafeReferences(t *testing.T) {
	t.Parallel()

	client := clientWithoutNetwork(t, "https://core.example/api/")
	tests := []struct {
		endpoint string
		want     string
	}{
		{endpoint: "", want: "must not be empty"},
		{endpoint: "/v1/nodes", want: "must be relative"},
		{endpoint: "//evil.example/nodes", want: "must be relative"},
		{endpoint: "https://evil.example/nodes", want: "override"},
		{endpoint: "v1/../admin", want: "path traversal"},
		{endpoint: "v1/%2e%2e/admin", want: "path traversal"},
		{endpoint: "v1/nodes#secret", want: "fragment"},
		{endpoint: "http://[::1", want: "parse Core endpoint"},
	}
	for _, test := range tests {
		_, err := client.resolveEndpoint(test.endpoint)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("resolveEndpoint(%q) error = %v, want containing %q", test.endpoint, err, test.want)
		}
	}
}

func TestTLSRequiresTrustedCAAndCorrectHostname(t *testing.T) {
	t.Parallel()

	server := newTLSServer(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(writer, `{}`)
	}))

	untrusted, err := New(Options{BaseURL: server.URL, Timeout: time.Second, UserAgent: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := untrusted.DoJSON(context.Background(), http.MethodGet, "status", nil, &struct{}{}); err == nil {
		t.Fatal("DoJSON() with untrusted server error = nil")
	}

	trusted := clientForServer(t, server, "/", time.Second)
	if err := trusted.DoJSON(context.Background(), http.MethodGet, "status", nil, &struct{}{}); err != nil {
		t.Fatalf("DoJSON() with private CA error = %v", err)
	}

	serverURL, _ := url.Parse(server.URL)
	_, port, _ := strings.Cut(serverURL.Host, ":")
	wrongHostURL := "https://localhost:" + port
	wrongHostOptions := Options{BaseURL: wrongHostURL, Timeout: time.Second, CAFile: certificateFile(t, server), UserAgent: "agent"}
	wrongHost, err := New(wrongHostOptions)
	if err != nil {
		t.Fatal(err)
	}
	if err := wrongHost.DoJSON(context.Background(), http.MethodGet, "status", nil, &struct{}{}); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("DoJSON() wrong-host error = %v, want certificate error", err)
	}
}

func TestDoJSONHandlesSuccessfulEmptyResponse(t *testing.T) {
	t.Parallel()

	server := newTLSServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Content-Type") != "" {
			t.Errorf("Content-Type = %q for nil body", request.Header.Get("Content-Type"))
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	client := clientForServer(t, server, "/", time.Second)
	if err := client.DoJSON(context.Background(), http.MethodDelete, "v1/node", nil, nil); err != nil {
		t.Fatalf("DoJSON() error = %v", err)
	}
}

func TestDoJSONReturnsTypedHTTPErrorAndDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var targetHits atomic.Int32
	server := newTLSServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(writer, request, "/target", http.StatusFound)
		case "/target":
			targetHits.Add(1)
		case "/failure":
			writer.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(writer, " invalid request \xff ")
		}
	}))
	client := clientForServer(t, server, "/", time.Second)

	err := client.DoJSON(context.Background(), http.MethodGet, "failure", nil, nil)
	var statusError *HTTPError
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(statusError.Body, "invalid request") {
		t.Fatalf("DoJSON() error = %#v, want typed HTTP 422 error", err)
	}

	err = client.DoJSON(context.Background(), http.MethodGet, "redirect", nil, nil)
	if !errors.As(err, &statusError) || statusError.StatusCode != http.StatusFound || targetHits.Load() != 0 {
		t.Fatalf("redirect error = %#v, target hits = %d", err, targetHits.Load())
	}
}

func TestDoJSONRejectsLargeAndInvalidResponses(t *testing.T) {
	t.Parallel()

	server := newTLSServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/large":
			io.WriteString(writer, strings.Repeat("x", MaxResponseBytes+1))
		case "/malformed":
			io.WriteString(writer, `{`)
		case "/trailing":
			io.WriteString(writer, `{} {}`)
		case "/empty":
			writer.WriteHeader(http.StatusOK)
		}
	}))
	client := clientForServer(t, server, "/", 2*time.Second)

	if err := client.DoJSON(context.Background(), http.MethodGet, "large", nil, nil); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("large response error = %v", err)
	}
	if err := client.DoJSON(context.Background(), http.MethodGet, "malformed", nil, &struct{}{}); err == nil || !strings.Contains(err.Error(), "decode Core response JSON") {
		t.Fatalf("malformed response error = %v", err)
	}
	if err := client.DoJSON(context.Background(), http.MethodGet, "trailing", nil, &struct{}{}); err == nil || !strings.Contains(err.Error(), "trailing JSON value") {
		t.Fatalf("trailing response error = %v", err)
	}
	if err := client.DoJSON(context.Background(), http.MethodGet, "empty", nil, &struct{}{}); err == nil || !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("empty typed response error = %v", err)
	}
}

func TestDoJSONReturnsEncodingCancellationAndTimeoutErrors(t *testing.T) {
	t.Parallel()

	server := newTLSServer(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow" {
			<-request.Context().Done()
			return
		}
		fmt.Fprint(writer, `{}`)
	}))
	client := clientForServer(t, server, "/", 20*time.Millisecond)

	if err := client.DoJSON(context.Background(), http.MethodPost, "status", make(chan int), nil); err == nil || !strings.Contains(err.Error(), "encode Core request JSON") {
		t.Fatalf("encoding error = %v", err)
	}

	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.DoJSON(cancelledContext, http.MethodGet, "status", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if err := client.DoJSON(context.Background(), http.MethodGet, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	if err := client.DoJSON(context.Background(), "BAD\nMETHOD", "status", nil, nil); err == nil || !strings.Contains(err.Error(), "create Core request") {
		t.Fatalf("invalid method error = %v", err)
	}
}

func TestHTTPErrorExcerptIsBoundedAndSanitized(t *testing.T) {
	t.Parallel()

	data := append([]byte(strings.Repeat("a", MaxErrorExcerptBytes)), 0xff, 'z')
	excerpt := errorExcerpt(data)
	if len(excerpt) != MaxErrorExcerptBytes {
		t.Fatalf("excerpt length = %d, want %d", len(excerpt), MaxErrorExcerptBytes)
	}
	if !strings.HasPrefix((&HTTPError{StatusCode: 500, Body: excerpt}).Error(), "LTS Core returned HTTP 500") {
		t.Fatal("HTTPError string lacks status")
	}
	if got := (&HTTPError{StatusCode: 404}).Error(); got != "LTS Core returned HTTP 404" {
		t.Fatalf("empty HTTPError = %q", got)
	}
}

func clientWithoutNetwork(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := New(Options{BaseURL: baseURL, Timeout: time.Second, UserAgent: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func clientForServer(t *testing.T, server *httptest.Server, basePath string, timeout time.Duration) *Client {
	t.Helper()
	client, err := New(Options{
		BaseURL:   server.URL + basePath,
		Timeout:   timeout,
		CAFile:    certificateFile(t, server),
		UserAgent: "lts-agent/0.5.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func certificateFile(t *testing.T, server *httptest.Server) string {
	t.Helper()
	certificate := server.Certificate()
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	path := filepath.Join(t.TempDir(), "core-ca.pem")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}
