// Package transport builds direct, HTTP proxy, and SOCKS5 HTTP transports.
package transport

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Mode string

const (
	Direct    Mode = "direct"
	HTTPProxy Mode = "http"
	SOCKS5    Mode = "socks5"
)

type Config struct {
	Mode          Mode
	ProxyURL      string
	Timeout       time.Duration
	TLSConfig     *tls.Config
	ProxyUsername string
	ProxyPassword string
}

func New(cfg Config) (http.RoundTripper, error) {
	if cfg.Mode == "" {
		cfg.Mode = Direct
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: cfg.Timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   cfg.Timeout,
		ResponseHeaderTimeout: cfg.Timeout,
		IdleConnTimeout:       90 * time.Second,
		TLSClientConfig:       cfg.TLSConfig,
	}
	switch cfg.Mode {
	case Direct:
		if cfg.ProxyURL != "" {
			return nil, errors.New("direct transport does not accept proxy URL")
		}
	case HTTPProxy:
		proxyURL, err := parseProxyURL(cfg.ProxyURL, "http", "https")
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	case SOCKS5:
		proxyURL, err := parseProxyURL(cfg.ProxyURL, "socks5", "socks5h")
		if err != nil {
			return nil, err
		}
		d := socks5Dialer{address: proxyURL.Host, base: dialer, username: cfg.ProxyUsername, password: cfg.ProxyPassword}
		transport.DialContext = d.DialContext
	default:
		return nil, fmt.Errorf("unsupported transport mode %q", cfg.Mode)
	}
	return transport, nil
}

func parseProxyURL(raw string, schemes ...string) (*url.URL, error) {
	if strings.ContainsAny(raw, "\r\n") {
		return nil, errors.New("proxy URL contains CRLF")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid proxy URL %q", raw)
	}
	for _, scheme := range schemes {
		if u.Scheme == scheme {
			return u, nil
		}
	}
	return nil, fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
}

type socks5Dialer struct {
	address  string
	base     *net.Dialer
	username string
	password string
}

func (d socks5Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := d.base.DialContext(ctx, "tcp", d.address)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err = socks5Connect(conn, address, d.username, d.password); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func socks5Connect(conn net.Conn, address, username, password string) error {
	methods := []byte{0}
	if username != "" || password != "" {
		if len(username) > 255 || len(password) > 255 {
			return errors.New("SOCKS5 credentials are too long")
		}
		methods = append(methods, 2)
	}
	greeting := append([]byte{5, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		return err
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response[:2]); err != nil {
		return err
	}
	if response[0] != 5 || (response[1] != 0 && response[1] != 2) {
		return errors.New("SOCKS5 proxy rejected authentication method")
	}
	if response[1] == 2 {
		auth := []byte{1, byte(len(username))}
		auth = append(auth, username...)
		auth = append(auth, byte(len(password)))
		auth = append(auth, password...)
		if _, err := conn.Write(auth); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, response[:2]); err != nil {
			return err
		}
		if response[1] != 0 {
			return errors.New("SOCKS5 proxy authentication failed")
		}
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid destination port %q", portText)
	}
	request := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 1)
			request = append(request, ip4...)
		} else {
			request = append(request, 4)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("destination hostname is too long")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, host...)
	}
	portBytes := []byte{0, 0}
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	request = append(request, portBytes...)
	if _, err = conn.Write(request); err != nil {
		return err
	}
	if _, err = io.ReadFull(conn, response); err != nil {
		return err
	}
	if response[0] != 5 || response[1] != 0 {
		return fmt.Errorf("SOCKS5 connect failed with code %d", response[1])
	}
	var addressLength int
	switch response[3] {
	case 1:
		addressLength = 4
	case 4:
		addressLength = 16
	case 3:
		length := []byte{0}
		if _, err = io.ReadFull(conn, length); err != nil {
			return err
		}
		addressLength = int(length[0])
	default:
		return errors.New("SOCKS5 proxy returned unknown address type")
	}
	_, err = io.CopyN(io.Discard, conn, int64(addressLength+2))
	return err
}
