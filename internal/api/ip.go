package api

import (
	"net"

	"github.com/labstack/echo/v5"
)

// ProxyAwareIPExtractor returns an echo IPExtractor that honors
// X-Forwarded-For ONLY when the direct TCP peer sits inside one of the
// given trusted proxy CIDRs. Echo's XFF extractor walks the header chain
// from the peer leftward and returns the first untrusted IP, so a
// spoofed XFF from an untrusted client can never influence the result:
// the walk stops at the (untrusted) direct peer itself.
//
// The trust defaults (loopback/link-local/private) are explicitly
// disabled — ONLY the configured CIDRs are trusted. Deployments behind
// a local reverse proxy add 127.0.0.1/32 (or the proxy's net)
// explicitly via TRUSTED_PROXY_CIDRS.
//
// Callers leave e.IPExtractor unset when nets is empty: echo then uses
// the direct peer unconditionally ("trust none").
func ProxyAwareIPExtractor(nets []*net.IPNet) echo.IPExtractor {
	opts := []echo.TrustOption{
		echo.TrustLoopback(false),
		echo.TrustLinkLocal(false),
		echo.TrustPrivateNet(false),
	}
	for _, n := range nets {
		opts = append(opts, echo.TrustIPRange(n))
	}
	return echo.ExtractIPFromXFFHeader(opts...)
}
