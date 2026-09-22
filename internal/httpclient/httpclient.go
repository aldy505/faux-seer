// Package httpclient builds the HTTP client used for every outbound provider
// call.
//
// The timeout is not optional. A provider that accepts a connection and never
// answers would otherwise hold the caller forever: for the explorer that means a
// run that stays in "processing" and a Sentry UI that polls, sees no assistant
// block, and gives up. The dial timeout is fixed and shorter than the overall
// timeout so a host that drops packets (firewall, missing route) fails in
// seconds instead of waiting out the whole budget on the kernel's SYN retries.
package httpclient

import (
	"net"
	"net/http"
	"time"

	"github.com/aldy505/faux-seer/internal/config"
)

// dialTimeout bounds connecting to a provider host.
const dialTimeout = 10 * time.Second

// New returns a client that gives up after cfg.OutboundTimeout.
func New(cfg *config.Config) *http.Client {
	timeout := config.DefaultOutboundTimeout
	if cfg != nil && cfg.OutboundTimeout > 0 {
		timeout = cfg.OutboundTimeout
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        32,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: dialTimeout,
		},
	}
}
