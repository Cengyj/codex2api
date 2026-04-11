package proxyutil

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	xproxy "golang.org/x/net/proxy"
)

type contextDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// NewProxyDialer builds a raw TCP dialer for HTTP(S), SOCKS4, SOCKS5, and SOCKS5H proxy URLs.
func NewProxyDialer(rawProxyURL string, baseDialer *net.Dialer) (xproxy.Dialer, error) {
	if baseDialer == nil {
		baseDialer = &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	}

	u, err := url.Parse(strings.TrimSpace(rawProxyURL))
	if err != nil {
		return nil, fmt.Errorf("parse proxy url: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
	case "http", "https":
		return newHTTPConnectDialer(u, baseDialer), nil
	case "socks4":
		return newSOCKS4Dialer(u, baseDialer), nil
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &xproxy.Auth{User: u.User.Username(), Password: password}
		}
		dialer, err := xproxy.SOCKS5("tcp", u.Host, auth, baseDialer)
		if err != nil {
			return nil, fmt.Errorf("build socks5 dialer: %w", err)
		}
		return dialer, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", u.Scheme)
	}
}

type httpConnectDialer struct {
	proxyAddr  string
	authHeader string
	forward    contextDialer
}

func newHTTPConnectDialer(u *url.URL, baseDialer contextDialer) *httpConnectDialer {
	addr := u.Host
	if !strings.Contains(addr, ":") {
		if strings.EqualFold(u.Scheme, "https") {
			addr += ":443"
		} else {
			addr += ":80"
		}
	}

	dialer := &httpConnectDialer{
		proxyAddr: addr,
		forward:   baseDialer,
	}
	if u.User != nil {
		username := u.User.Username()
		password, _ := u.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		dialer.authHeader = "Basic " + credentials
	}
	return dialer
}

func (d *httpConnectDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *httpConnectDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := d.forward.DialContext(ctx, "tcp", d.proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect proxy server: %w", err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}

	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", address, address)
	if d.authHeader != "" {
		connectReq += fmt.Sprintf("Proxy-Authorization: %s\r\n", d.authHeader)
	}
	connectReq += "\r\n"

	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write CONNECT request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT failed (status %d)", resp.StatusCode)
	}

	if br.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: br}, nil
	}
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type socks4Dialer struct {
	proxyAddr string
	userID    string
	forward   contextDialer
}

func newSOCKS4Dialer(u *url.URL, baseDialer contextDialer) *socks4Dialer {
	userID := ""
	if u.User != nil {
		userID = u.User.Username()
	}
	return &socks4Dialer{
		proxyAddr: u.Host,
		userID:    userID,
		forward:   baseDialer,
	}
}

func (d *socks4Dialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *socks4Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(network)), "tcp") {
		return nil, fmt.Errorf("socks4 only supports tcp, got %s", network)
	}

	host, portValue, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split target address: %w", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid target port")
	}

	conn, err := d.forward.DialContext(ctx, "tcp", d.proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("connect socks4 proxy: %w", err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}

	request := make([]byte, 0, 9+len(d.userID)+len(host))
	request = append(request, 0x04, 0x01)
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, uint16(port))
	request = append(request, portBytes...)

	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		request = append(request, ip.To4()...)
	} else {
		request = append(request, 0x00, 0x00, 0x00, 0x01)
	}

	request = append(request, d.userID...)
	request = append(request, 0x00)

	if ip := net.ParseIP(host); ip == nil || ip.To4() == nil {
		request = append(request, host...)
		request = append(request, 0x00)
	}

	if _, err := conn.Write(request); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write socks4 request: %w", err)
	}

	reply := make([]byte, 8)
	if _, err := io.ReadFull(conn, reply); err != nil {
		conn.Close()
		return nil, fmt.Errorf("read socks4 response: %w", err)
	}
	if reply[1] != 0x5a {
		conn.Close()
		return nil, fmt.Errorf("socks4 proxy rejected request with code 0x%02x", reply[1])
	}

	return conn, nil
}
