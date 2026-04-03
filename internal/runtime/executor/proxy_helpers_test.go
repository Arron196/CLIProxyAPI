package executor

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

func setEnvironmentProxy(t *testing.T, proxyURL string) {
	t.Helper()

	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		oldValue, hadValue := os.LookupEnv(key)
		if err := os.Setenv(key, proxyURL); err != nil {
			t.Fatalf("Setenv(%s): %v", key, err)
		}
		cleanupKey := key
		cleanupOldValue := oldValue
		cleanupHadValue := hadValue
		t.Cleanup(func() {
			if cleanupHadValue {
				_ = os.Setenv(cleanupKey, cleanupOldValue)
				return
			}
			_ = os.Unsetenv(cleanupKey)
		})
	}
}

func TestNewProxyAwareHTTPClientDirectBypassesGlobalProxy(t *testing.T) {
	t.Parallel()

	client := newProxyAwareHTTPClient(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "direct"},
		0,
	)

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestNewProxyAwareHTTPClientNoProxyReusesSharedClient(t *testing.T) {
	t.Parallel()

	first := newProxyAwareHTTPClient(context.Background(), nil, nil, 0)
	second := newProxyAwareHTTPClient(context.Background(), nil, nil, 0)

	if first == second {
		t.Fatal("expected distinct client wrappers for direct no-proxy path")
	}
	if first.Transport != nil {
		t.Fatalf("expected direct client to use default transport, got %T", first.Transport)
	}
	if second.Transport != nil {
		t.Fatalf("expected second direct client to use default transport, got %T", second.Transport)
	}
}

func TestNewProxyAwareHTTPClientProxyReusesCachedClientWithoutTimeout(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://proxy.example.com:8080"}}
	first := newProxyAwareHTTPClient(context.Background(), cfg, nil, 0)
	second := newProxyAwareHTTPClient(context.Background(), cfg, nil, 0)

	if first == second {
		t.Fatal("expected proxy calls to receive distinct client wrappers")
	}
	transport, ok := first.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", first.Transport)
	}
	if second.Transport != transport {
		t.Fatal("expected proxy transport to be reused across client wrappers")
	}
	proxyFunc := transport.Proxy
	if proxyFunc == nil {
		t.Fatal("expected proxy transport to configure proxy function")
	}
	targetURL, errParse := url.Parse("https://example.com")
	if errParse != nil {
		t.Fatalf("url.Parse() error = %v", errParse)
	}
	gotProxy, errProxy := proxyFunc(&http.Request{URL: targetURL})
	if errProxy != nil {
		t.Fatalf("proxy function error = %v", errProxy)
	}
	if gotProxy == nil || gotProxy.String() != "http://proxy.example.com:8080" {
		t.Fatalf("proxy = %v, want http://proxy.example.com:8080", gotProxy)
	}
}

func TestNewProxyAwareHTTPClientFallsBackToEnvironmentProxy(t *testing.T) {
	setEnvironmentProxy(t, "http://env-proxy.example.com:8080")

	client := newProxyAwareHTTPClient(context.Background(), &config.Config{}, &cliproxyauth.Auth{}, 0)

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("expected environment proxy transport to configure Proxy function")
	}
	req, errReq := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if errReq != nil {
		t.Fatalf("NewRequest() error = %v", errReq)
	}
	proxyURL, errProxy := transport.Proxy(req)
	if errProxy != nil {
		t.Fatalf("transport.Proxy() error = %v", errProxy)
	}
	if proxyURL == nil || proxyURL.String() != "http://env-proxy.example.com:8080" {
		t.Fatalf("proxy URL = %v, want http://env-proxy.example.com:8080", proxyURL)
	}
}

func TestNewProxyAwareHTTPClientExplicitProxyWinsOverEnvironmentProxy(t *testing.T) {
	setEnvironmentProxy(t, "http://env-proxy.example.com:8080")

	client := newProxyAwareHTTPClient(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://config-proxy.example.com:8080"}},
		nil,
		0,
	)

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	req, errReq := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if errReq != nil {
		t.Fatalf("NewRequest() error = %v", errReq)
	}
	proxyURL, errProxy := transport.Proxy(req)
	if errProxy != nil {
		t.Fatalf("transport.Proxy() error = %v", errProxy)
	}
	if proxyURL == nil || proxyURL.String() != "http://config-proxy.example.com:8080" {
		t.Fatalf("proxy URL = %v, want http://config-proxy.example.com:8080", proxyURL)
	}
}

func TestNewProxyAwareHTTPClientReusesEnvironmentProxyTransport(t *testing.T) {
	setEnvironmentProxy(t, "http://env-proxy.example.com:8080")

	clientA := newProxyAwareHTTPClient(context.Background(), &config.Config{}, &cliproxyauth.Auth{}, 0)
	clientB := newProxyAwareHTTPClient(context.Background(), &config.Config{}, &cliproxyauth.Auth{}, 0)

	transportA, okA := clientA.Transport.(*http.Transport)
	if !okA {
		t.Fatalf("clientA transport type = %T, want *http.Transport", clientA.Transport)
	}
	transportB, okB := clientB.Transport.(*http.Transport)
	if !okB {
		t.Fatalf("clientB transport type = %T, want *http.Transport", clientB.Transport)
	}
	if transportA != transportB {
		t.Fatal("expected environment proxy transport to be shared across clients")
	}
}

func TestNewProxyAwareHTTPClientNoProxyDoesNotLeakAntigravityTransportMutation(t *testing.T) {
	client := newAntigravityHTTPClient(context.Background(), nil, nil, 0)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("antigravity transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.ForceAttemptHTTP2 {
		t.Fatal("expected antigravity transport to disable HTTP/2")
	}

	regular := newProxyAwareHTTPClient(context.Background(), nil, nil, 0)
	if regular.Transport != nil {
		t.Fatalf("regular client transport = %T, want nil default transport", regular.Transport)
	}
}

func TestNewProxyAwareHTTPClientProxyDoesNotLeakAntigravityTransportMutation(t *testing.T) {
	cfg := &config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://proxy.example.com:8080"}}

	antigravityClient := newAntigravityHTTPClient(context.Background(), cfg, nil, 0)
	antigravityTransport, ok := antigravityClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("antigravity transport type = %T, want *http.Transport", antigravityClient.Transport)
	}
	if antigravityTransport.TLSClientConfig == nil || len(antigravityTransport.TLSClientConfig.NextProtos) != 1 || antigravityTransport.TLSClientConfig.NextProtos[0] != "http/1.1" {
		t.Fatalf("expected antigravity transport to force HTTP/1.1 ALPN, got %#v", antigravityTransport.TLSClientConfig)
	}

	regular := newProxyAwareHTTPClient(context.Background(), cfg, nil, 0)
	regularTransport, ok := regular.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("regular transport type = %T, want *http.Transport", regular.Transport)
	}
	if regularTransport == antigravityTransport {
		t.Fatal("expected regular proxy client to keep cached transport instead of antigravity clone")
	}
	if regularTransport.TLSClientConfig != nil && len(regularTransport.TLSClientConfig.NextProtos) == 1 && regularTransport.TLSClientConfig.NextProtos[0] == "http/1.1" {
		t.Fatalf("regular transport unexpectedly inherited antigravity HTTP/1.1-only ALPN override: %#v", regularTransport.TLSClientConfig.NextProtos)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewProxyAwareHTTPClientInvalidProxyFallsBackToContextTransport(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Request: req}, nil
	}))

	client := newProxyAwareHTTPClient(ctx, &config.Config{
		SDKConfig: sdkconfig.SDKConfig{ProxyURL: "://bad-proxy"},
	}, nil, 0)

	if client.Transport == nil {
		t.Fatal("expected context transport fallback when proxy build fails")
	}
	if _, ok := client.Transport.(roundTripperFunc); !ok {
		t.Fatalf("transport type = %T, want context roundTripperFunc fallback", client.Transport)
	}
}
