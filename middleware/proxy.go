package middleware

import (
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webx-top/echo"
)

// TODO: Handle TLS proxy

type (
	// ProxyConfig defines the config for Proxy middleware.
	ProxyConfig struct {
		// Skipper defines a function to skip middleware.
		Skipper echo.Skipper `json:"-"`

		// Balancer defines a load balancing technique.
		// Required.
		Balancer ProxyBalancer `json:"-"`

		Handler ProxyHandler `json:"-"`
		Rewrite RewriteConfig

		// Context key to store selected ProxyTarget into context.
		// Optional. Default value "target".
		ContextKey string
	}

	// ProxyTarget defines the upstream target.
	ProxyTarget struct {
		Name          string
		URL           *url.URL
		FlushInterval time.Duration
		Meta          echo.Store
	}

	// ProxyBalancer defines an interface to implement a load balancing technique.
	ProxyBalancer interface {
		AddTarget(*ProxyTarget) bool
		RemoveTarget(string) bool
		Next(echo.Context) *ProxyTarget
	}

	// ProxyHandler defines an interface to implement a proxy handler.
	ProxyHandler func(t *ProxyTarget, c echo.Context) error

	commonBalancer struct {
		targets []*ProxyTarget
		mutex   sync.RWMutex
	}

	// RandomBalancer implements a random load balancing technique.
	randomBalancer struct {
		*commonBalancer
		random *rand.Rand
	}

	// RoundRobinBalancer implements a round-robin load balancing technique.
	roundRobinBalancer struct {
		*commonBalancer
		i uint32
	}
)

var (
	// DefaultProxyConfig is the default Proxy middleware config.
	DefaultProxyConfig = ProxyConfig{
		Skipper:    echo.DefaultSkipper,
		Handler:    DefaultProxyHandler,
		Rewrite:    DefaultRewriteConfig,
		ContextKey: "target",
	}
	// DefaultProxyHandler Proxy Handler
	DefaultProxyHandler ProxyHandler = func(t *ProxyTarget, c echo.Context) error {
		resp := c.Response().StdResponseWriter()
		req := c.Request().StdRequest()
		switch {
		case c.IsWebsocket():
			proxyRaw(t, c).ServeHTTP(resp, req)
		case c.Header(echo.HeaderAccept) == "text/event-stream":
			proxyHTTPWithFlushInterval(t).ServeHTTP(resp, req)
		default:
			proxyHTTP(t, c).ServeHTTP(resp, req)
		}
		return nil
	}
)

// Server-Sent Events
func proxyHTTPWithFlushInterval(t *ProxyTarget) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(t.URL)
	proxy.FlushInterval = t.FlushInterval
	return proxy
}

// http
func proxyHTTP(t *ProxyTarget, _ echo.Context) http.Handler {
	return httputil.NewSingleHostReverseProxy(t.URL)
}

// websocket
func proxyRaw(t *ProxyTarget, c echo.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			c.Error(fmt.Errorf("proxy raw, hijack error=%v, url=%s", t.URL, err))
			return
		}
		defer in.Close()

		out, err := net.Dial("tcp", t.URL.Host)
		if err != nil {
			he := echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("proxy raw, dial error=%v, url=%s", t.URL, err)).SetRaw(err)
			c.Error(he)
			return
		}
		defer out.Close()

		// Write header
		err = r.Write(out)
		if err != nil {
			he := echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("proxy raw, request header copy error=%v, url=%s", t.URL, err)).SetRaw(err)
			c.Error(he)
			return
		}

		errCh := make(chan error, 2)
		cp := func(dst io.Writer, src io.Reader) {
			_, err = io.Copy(dst, src)
			errCh <- err
		}

		go cp(out, in)
		go cp(in, out)
		err = <-errCh
		if err != nil && err != io.EOF {
			c.Logger().Errorf("proxy raw, copy body error=%v, url=%s", t.URL, err)
		}
	})
}

// NewRandomBalancer returns a random proxy balancer.
func NewRandomBalancer(targets []*ProxyTarget) ProxyBalancer {
	b := &randomBalancer{commonBalancer: new(commonBalancer)}
	b.targets = targets
	return b
}

// NewRoundRobinBalancer returns a round-robin proxy balancer.
func NewRoundRobinBalancer(targets []*ProxyTarget) ProxyBalancer {
	b := &roundRobinBalancer{commonBalancer: new(commonBalancer)}
	b.targets = targets
	return b
}

// AddTarget adds an upstream target to the list.
func (b *commonBalancer) AddTarget(target *ProxyTarget) bool {
	for _, t := range b.targets {
		if t.Name == target.Name {
			return false
		}
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()

	if target.FlushInterval <= 0 {
		target.FlushInterval = 100 * time.Millisecond
	}

	b.targets = append(b.targets, target)
	return true
}

// RemoveTarget removes an upstream target from the list.
func (b *commonBalancer) RemoveTarget(name string) bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	for i, t := range b.targets {
		if t.Name == name {
			b.targets = append(b.targets[:i], b.targets[i+1:]...)
			return true
		}
	}
	return false
}

// Next randomly returns an upstream target.
func (b *randomBalancer) Next(c echo.Context) *ProxyTarget {
	if b.random == nil {
		b.random = rand.New(rand.NewSource(int64(time.Now().Nanosecond())))
	}
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return b.targets[b.random.Intn(len(b.targets))]
}

// Next returns an upstream target using round-robin technique.
func (b *roundRobinBalancer) Next(c echo.Context) *ProxyTarget {
	b.i = b.i % uint32(len(b.targets))
	t := b.targets[b.i]
	atomic.AddUint32(&b.i, 1)
	return t
}

// Proxy returns a Proxy middleware.
//
// Proxy middleware forwards the request to upstream server using a configured load balancing technique.
func Proxy(balancer ProxyBalancer) echo.MiddlewareFuncd {
	c := DefaultProxyConfig
	c.Balancer = balancer
	return ProxyWithConfig(c)
}

// ProxyWithConfig returns a Proxy middleware with config.
// See: `Proxy()`
func ProxyWithConfig(config ProxyConfig) echo.MiddlewareFuncd {
	// Defaults
	if config.Skipper == nil {
		config.Skipper = DefaultProxyConfig.Skipper
	}
	if config.Handler == nil {
		config.Handler = DefaultProxyConfig.Handler
	}
	if config.Balancer == nil {
		panic("echo: proxy middleware requires balancer")
	}
	config.Rewrite.Init()
	return func(next echo.Handler) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			if config.Skipper(c) {
				return next.Handle(c)
			}

			req := c.Request()
			tgt := config.Balancer.Next(c)
			if len(config.ContextKey) > 0 {
				c.Set(config.ContextKey, tgt)
			}
			req.URL().SetPath(config.Rewrite.Rewrite(req.URL().Path()))
			// Fix header
			if len(c.Header(echo.HeaderXRealIP)) == 0 {
				req.Header().Set(echo.HeaderXRealIP, c.RealIP())
			}
			if len(c.Header(echo.HeaderXForwardedProto)) == 0 {
				req.Header().Set(echo.HeaderXForwardedProto, c.Scheme())
			}
			if c.IsWebsocket() && len(c.Header(echo.HeaderXForwardedFor)) == 0 { // For HTTP, it is automatically set by Go HTTP reverse proxy.
				req.Header().Set(echo.HeaderXForwardedFor, c.RealIP())
			}

			return config.Handler(tgt, c)
		}
	}
}
