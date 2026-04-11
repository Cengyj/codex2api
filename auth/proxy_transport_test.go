package auth

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestConfigureTransportProxyHTTPProxy(t *testing.T) {
	transport := &http.Transport{}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	if err := ConfigureTransportProxy(transport, "http://127.0.0.1:8080", baseDialer); err != nil {
		t.Fatalf("ConfigureTransportProxy() error = %v", err)
	}
	if transport.Proxy == nil {
		t.Fatal("expected HTTP proxy handler to be configured")
	}
	if transport.DialContext == nil {
		t.Fatal("expected HTTP proxy to preserve the base dialer")
	}
}

func TestConfigureTransportProxySOCKS5Proxy(t *testing.T) {
	transport := &http.Transport{}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	if err := ConfigureTransportProxy(transport, "socks5://127.0.0.1:1080", baseDialer); err != nil {
		t.Fatalf("ConfigureTransportProxy() error = %v", err)
	}
	if transport.Proxy != nil {
		t.Fatal("expected SOCKS5 proxy to bypass transport.Proxy")
	}
	if transport.DialContext == nil {
		t.Fatal("expected SOCKS5 proxy dialer to be installed")
	}
}

func TestConfigureTransportProxySOCKS4Proxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	requests := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		request := make([]byte, 9)
		if _, err := io.ReadFull(conn, request); err != nil {
			return
		}
		requests <- request
		_, _ = conn.Write([]byte{0x00, 0x5a, 0x01, 0xbb, 0x01, 0x02, 0x03, 0x04})
	}()

	transport := &http.Transport{}
	baseDialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}

	if err := ConfigureTransportProxy(transport, "socks4://"+listener.Addr().String(), baseDialer); err != nil {
		t.Fatalf("ConfigureTransportProxy() error = %v", err)
	}
	if transport.Proxy != nil {
		t.Fatal("expected SOCKS4 proxy to bypass transport.Proxy")
	}
	if transport.DialContext == nil {
		t.Fatal("expected SOCKS4 proxy dialer to be installed")
	}

	conn, err := transport.DialContext(context.Background(), "tcp", "1.2.3.4:443")
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	_ = conn.Close()

	select {
	case request := <-requests:
		if request[0] != 0x04 || request[1] != 0x01 {
			t.Fatalf("unexpected SOCKS4 header: %v", request[:2])
		}
		if request[2] != 0x01 || request[3] != 0xbb {
			t.Fatalf("unexpected SOCKS4 port bytes: %v", request[2:4])
		}
		if request[4] != 0x01 || request[5] != 0x02 || request[6] != 0x03 || request[7] != 0x04 {
			t.Fatalf("unexpected SOCKS4 address bytes: %v", request[4:8])
		}
		if request[8] != 0x00 {
			t.Fatalf("unexpected SOCKS4 user terminator byte: %v", request[8])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SOCKS4 handshake")
	}
}
