package app

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func normalizeSourceProxyURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if !strings.Contains(value, "://") {
		value = "socks5://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Port() == "" {
		return "", errors.New("proxy must include a host and port")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("proxy must contain only a host and a valid port")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "socks5", "socks5h", "http", "https":
		return parsed.String(), nil
	default:
		return "", errors.New("proxy must use socks5, http or https")
	}
}

func (a *App) sourceHTTPClient(proxyURL string) (*http.Client, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return a.sourceClient, nil
	}
	a.proxyMu.Lock()
	defer a.proxyMu.Unlock()
	if client := a.proxyClients[proxyURL]; client != nil {
		return client, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, errors.New("invalid source proxy")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{ResponseHeaderTimeout: 20 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true, MaxConnsPerHost: 32, MaxIdleConns: 100, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second}
	var tunnelDial func(context.Context, string, string) (net.Conn, error)
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		tunnelDial = httpTunnelDialer(parsed, dialer)
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: parsed.User.Username(), Password: password}
		}
		socksDialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, dialer)
		if err != nil {
			return nil, err
		}
		if contextDialer, ok := socksDialer.(proxy.ContextDialer); ok {
			tunnelDial = contextDialer.DialContext
		} else {
			tunnelDial = func(_ context.Context, network, address string) (net.Conn, error) {
				return socksDialer.Dial(network, address)
			}
		}
	default:
		return nil, errors.New("unsupported source proxy")
	}
	transport.DialContext = guardedSourceDial(tunnelDial, a.cfg.AllowPrivateSources)
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}}
	if a.proxyClients == nil {
		a.proxyClients = make(map[string]*http.Client)
	}
	// Bound the cache when administrators change proxy configurations repeatedly.
	if len(a.proxyClients) >= 16 {
		for key, old := range a.proxyClients {
			old.CloseIdleConnections()
			delete(a.proxyClients, key)
		}
	}
	a.proxyClients[proxyURL] = client
	return client, nil
}

// HTTP CONNECT pins the destination IP even when the proxy normally resolves
// hostnames itself. TLS to the destination still uses the original URL hostname.
func httpTunnelDialer(endpoint *url.URL, dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		var conn net.Conn
		var err error
		if endpoint.Scheme == "https" {
			d := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: endpoint.Hostname()}}
			conn, err = d.DialContext(ctx, network, endpoint.Host)
		} else {
			conn, err = dialer.DialContext(ctx, network, endpoint.Host)
		}
		if err != nil {
			return nil, err
		}
		deadline := time.Now().Add(10 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		conn.SetDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: make(http.Header)}
		if endpoint.User != nil {
			password, _ := endpoint.User.Password()
			req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(endpoint.User.Username()+":"+password)))
		}
		if err = req.Write(conn); err != nil {
			stop()
			conn.Close()
			return nil, err
		}
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, req)
		if err != nil {
			stop()
			conn.Close()
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			stop()
			conn.Close()
			return nil, fmt.Errorf("proxy CONNECT returned %s", resp.Status)
		}
		if !stop() || ctx.Err() != nil {
			conn.Close()
			return nil, ctx.Err()
		}
		conn.SetDeadline(time.Time{})
		return &bufferedTunnelConn{Conn: conn, reader: reader}, nil
	}
}

type bufferedTunnelConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedTunnelConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func writeError(w http.ResponseWriter, status int, message string) {
	writeAPIError(w, status, message)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func normalizeEngineURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", errors.New("engine URL must use http or https")
	}
	return parsed.String(), nil
}

func newSafeHTTPClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{DialContext: guardedSourceDial(dialer.DialContext, allowPrivate), ResponseHeaderTimeout: 20 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: true, MaxConnsPerHost: 32, MaxIdleConns: 100, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}}
}

func guardedSourceDial(dial func(context.Context, string, string) (net.Conn, error), allowPrivate bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if blockedSourceIP(ip, allowPrivate) {
				return nil, fmt.Errorf("source address is not allowed")
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func blockedSourceIP(ip net.IP, allowPrivate bool) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	if ip.String() == "169.254.169.254" {
		return true
	}
	if !ip.IsGlobalUnicast() {
		return true
	}
	// Non-public special-purpose ranges in addition to RFC 1918/4193.
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001:db8::/32"} {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return !allowPrivate && ip.IsPrivate()
}

func readLimitedBody(response *http.Response, limit int64) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("remote server returned %s", response.Status)
	}
	reader := io.LimitReader(response.Body, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("playlist exceeds 10 MB")
	}
	return data, nil
}
