package executor

import (
	"context"
	"net/http"
	"os"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

var (
	proxyHTTPTransportCache      sync.Map // map[string]*cachedProxyTransport
	environmentProxyKeys         = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}
	environmentProxyTransportCache sync.Map // map[string]*http.Transport
)

type cachedProxyTransport struct {
	once      sync.Once
	transport *http.Transport
}

// newProxyAwareHTTPClient creates an HTTP client with proper proxy configuration priority:
// 1. Use auth.ProxyURL if configured (highest priority)
// 2. Use cfg.ProxyURL if auth proxy is not configured
// 3. Use environment proxy settings if neither are configured
// 4. Use RoundTripper from context if no explicit or environment proxy is configured
func newProxyAwareHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	var contextTransport http.RoundTripper
	if ctx != nil {
		if rt, ok := ctx.Value("cliproxy.roundtripper").(http.RoundTripper); ok && rt != nil {
			contextTransport = rt
		}
	}

	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	if proxyURL != "" {
		if transport := cachedTransportForProxyURL(proxyURL); transport != nil {
			return newProxyHTTPClient(transport, timeout)
		}
		log.Debugf("failed to setup proxy from URL: %s, falling back to context transport", proxyURL)
	}

	if environmentProxyConfigured() {
		return newProxyHTTPClient(newEnvironmentProxyTransport(), timeout)
	}

	if contextTransport != nil {
		return newProxyHTTPClient(contextTransport, timeout)
	}

	return newProxyHTTPClient(nil, timeout)
}

func cachedTransportForProxyURL(proxyURL string) *http.Transport {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil
	}
	entryAny, _ := proxyHTTPTransportCache.LoadOrStore(proxyURL, &cachedProxyTransport{})
	entry := entryAny.(*cachedProxyTransport)
	entry.once.Do(func() {
		entry.transport = buildProxyTransport(proxyURL)
	})
	return entry.transport
}

func newProxyHTTPClient(transport http.RoundTripper, timeout time.Duration) *http.Client {
	client := &http.Client{Transport: transport}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}

func buildProxyTransport(proxyURL string) *http.Transport {
	transport, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
	if errBuild != nil {
		log.Errorf("%v", errBuild)
		return nil
	}
	return transport
}

func environmentProxyConfigured() bool {
	for _, key := range environmentProxyKeys {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func newEnvironmentProxyTransport() *http.Transport {
	signature := environmentProxySignature()
	if cached, ok := environmentProxyTransportCache.Load(signature); ok {
		return cached.(*http.Transport)
	}

	proxyFunc := environmentProxyFunc()
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		clone := base.Clone()
		clone.Proxy = proxyFunc
		transport = clone
	} else {
		transport = &http.Transport{Proxy: proxyFunc}
	}
	actual, _ := environmentProxyTransportCache.LoadOrStore(signature, transport)
	return actual.(*http.Transport)
}

func environmentProxySignature() string {
	var values []string
	for _, key := range environmentProxyKeys {
		values = append(values, key+"="+strings.TrimSpace(os.Getenv(key)))
	}
	return strings.Join(values, "|")
}

func environmentProxyFunc() func(*http.Request) (*url.URL, error) {
	httpProxy := firstEnvironmentValue("HTTP_PROXY", "http_proxy")
	httpsProxy := firstEnvironmentValue("HTTPS_PROXY", "https_proxy")
	allProxy := firstEnvironmentValue("ALL_PROXY", "all_proxy")

	return func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil {
			return nil, nil
		}

		raw := ""
		switch strings.ToLower(req.URL.Scheme) {
		case "https":
			raw = firstNonEmpty(httpsProxy, allProxy, httpProxy)
		case "http":
			raw = firstNonEmpty(httpProxy, allProxy, httpsProxy)
		default:
			raw = firstNonEmpty(allProxy, httpsProxy, httpProxy)
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil, nil
		}
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		return url.Parse(raw)
	}
}

func firstEnvironmentValue(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
