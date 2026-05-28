package network

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram/dcs"
	"golang.org/x/net/proxy"
)

type ProxyKind string

const (
	ProxyNone    ProxyKind = "none"
	ProxySOCKS5  ProxyKind = "socks5"
	ProxyHTTP    ProxyKind = "http"
	ProxyMTProxy ProxyKind = "mtproxy"
)

type ProxySource string

const (
	SourceNone        ProxySource = "none"
	SourceManual      ProxySource = "manual"
	SourceEnvironment ProxySource = "environment"
)

type ProxyConfig struct {
	Kind     ProxyKind
	Source   ProxySource
	Address  string
	Username string
	Password string
	Secret   string
	EnvVar   string
}

type UIEntry struct {
	Name        string
	Source      ProxySource
	Kind        ProxyKind
	Description string
	Active      bool
}

func ResolveProxy(cliValue string, environ []string) (ProxyConfig, error) {
	if strings.TrimSpace(cliValue) != "" {
		cfg, err := ParseProxyURL(cliValue)
		if err != nil {
			return ProxyConfig{}, err
		}
		cfg.Source = SourceManual
		return cfg, nil
	}

	for _, key := range []string{
		"TSUMUGI_PROXY",
		"TSUMUGI_SOCKS5_PROXY",
		"TSUMUGI_HTTP_PROXY",
		"TSUMUGI_MTPROXY",
		"HTTPS_PROXY",
		"HTTP_PROXY",
		"ALL_PROXY",
		"https_proxy",
		"http_proxy",
		"all_proxy",
	} {
		if value, ok := lookupEnv(environ, key); ok && strings.TrimSpace(value) != "" {
			cfg, err := ParseProxyURL(value)
			if err != nil {
				return ProxyConfig{}, fmt.Errorf("parse %s: %w", key, err)
			}
			cfg.Source = SourceEnvironment
			cfg.EnvVar = key
			return cfg, nil
		}
	}

	return ProxyConfig{Kind: ProxyNone, Source: SourceNone}, nil
}

func ParseProxyURL(value string) (ProxyConfig, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") {
		return ProxyConfig{Kind: ProxyNone, Source: SourceManual}, nil
	}

	u, err := url.Parse(value)
	if err != nil {
		return ProxyConfig{}, err
	}
	if u.Scheme == "" {
		return ProxyConfig{}, errors.New("proxy URL must include a scheme")
	}

	cfg := ProxyConfig{
		Address: u.Host,
	}
	if u.User != nil {
		cfg.Username = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}

	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		cfg.Kind = ProxySOCKS5
	case "http", "https":
		cfg.Kind = ProxyHTTP
	case "mtproxy", "telegram-mtproxy":
		cfg.Kind = ProxyMTProxy
		cfg.Secret = strings.TrimPrefix(strings.TrimSpace(u.Query().Get("secret")), "0x")
		if cfg.Secret == "" && u.User != nil {
			cfg.Secret = cfg.Username
			cfg.Username = ""
		}
		if cfg.Secret == "" {
			cfg.Secret = strings.TrimPrefix(strings.Trim(u.Path, "/"), "0x")
		}
	default:
		return ProxyConfig{}, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}

	if cfg.Address == "" {
		return ProxyConfig{}, errors.New("proxy address is required")
	}
	if cfg.Kind == ProxyMTProxy && cfg.Secret == "" {
		return ProxyConfig{}, errors.New("MTProxy secret is required")
	}
	return cfg, nil
}

func (p ProxyConfig) Active() bool {
	return p.Kind != "" && p.Kind != ProxyNone
}

func (p ProxyConfig) MaskedAddress() string {
	if p.Address == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(p.Address)
	if err != nil {
		return p.Address
	}
	if p.Username == "" && p.Password == "" {
		return net.JoinHostPort(host, port)
	}
	return fmt.Sprintf("***@%s", net.JoinHostPort(host, port))
}

func (p ProxyConfig) UIEntries() []UIEntry {
	entries := []UIEntry{
		{Name: "No proxy", Source: SourceNone, Kind: ProxyNone, Description: "Connect directly", Active: !p.Active()},
	}
	if p.Source == SourceEnvironment {
		entries = append(entries, UIEntry{
			Name:        "Environment",
			Source:      SourceEnvironment,
			Kind:        p.Kind,
			Description: fmt.Sprintf("%s via %s (%s)", p.Kind, p.EnvVar, p.MaskedAddress()),
			Active:      true,
		})
		return entries
	}
	if p.Active() {
		entries = append(entries, UIEntry{
			Name:        "Configured proxy",
			Source:      SourceManual,
			Kind:        p.Kind,
			Description: fmt.Sprintf("%s via %s", p.Kind, p.MaskedAddress()),
			Active:      true,
		})
	}
	return entries
}

func (p ProxyConfig) Resolver() (dcs.Resolver, error) {
	switch p.Kind {
	case "", ProxyNone:
		return nil, nil
	case ProxySOCKS5:
		dialer, err := p.socks5Dialer()
		if err != nil {
			return nil, err
		}
		return dcs.Plain(dcs.PlainOptions{Dial: dialer.DialContext}), nil
	case ProxyHTTP:
		return dcs.Plain(dcs.PlainOptions{Dial: p.httpDialContext}), nil
	case ProxyMTProxy:
		secret, err := decodeMTProxySecret(p.Secret)
		if err != nil {
			return nil, err
		}
		return dcs.MTProxy(p.Address, secret, dcs.MTProxyOptions{})
	default:
		return nil, fmt.Errorf("unsupported proxy kind %q", p.Kind)
	}
}

func (p ProxyConfig) socks5Dialer() (proxy.ContextDialer, error) {
	var auth *proxy.Auth
	if p.Username != "" || p.Password != "" {
		auth = &proxy.Auth{User: p.Username, Password: p.Password}
	}
	dialer, err := proxy.SOCKS5("tcp", p.Address, auth, proxy.Direct)
	if err != nil {
		return nil, err
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("SOCKS5 dialer does not support context")
	}
	return contextDialer, nil
}

func (p ProxyConfig) httpDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("unsupported network %q for HTTP proxy", network)
	}

	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", p.Address)
	if err != nil {
		return nil, err
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if p.Username != "" || p.Password != "" {
		token := base64.StdEncoding.EncodeToString([]byte(p.Username + ":" + p.Password))
		req.Header.Set("Proxy-Authorization", "Basic "+token)
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = conn.Close()
		return nil, fmt.Errorf("HTTP proxy CONNECT failed: %s", resp.Status)
	}
	return conn, nil
}

func decodeMTProxySecret(secret string) ([]byte, error) {
	secret = strings.TrimPrefix(strings.TrimSpace(secret), "0x")
	if secret == "" {
		return nil, errors.New("empty MTProxy secret")
	}
	decoded, err := hex.DecodeString(secret)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func lookupEnv(environ []string, key string) (string, bool) {
	for _, item := range environ {
		k, v, ok := strings.Cut(item, "=")
		if ok && k == key {
			return v, true
		}
	}
	return "", false
}

func HostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}
