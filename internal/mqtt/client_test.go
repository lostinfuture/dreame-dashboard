package mqtt

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func TestTLSSession(t *testing.T) {
	cert := testCert(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		first, body, err := ReadPacket(br)
		if err != nil || first != 0x10 || !bytes.Contains(body, []byte("p_")) {
			return
		}
		if !bytes.Contains(body, []byte("user")) || !bytes.Contains(body, []byte("secret")) {
			return
		}
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		_, sub, err := ReadPacket(br)
		if err != nil || !bytes.Contains(sub, []byte("/status/1/u/m/us/")) {
			return
		}
		_, _ = conn.Write([]byte{0x90, 0x03, 0x00, 0x01, 0x00})
		payload := []byte(`{"method":"props","params":{"ota_state":"idle"}}`)
		pub := append([]byte{0x32}, encodeLen(2+1+2+len(payload))...)
		pub = append(pub, 0x00, 0x01, 't', 0x00, 0x09)
		pub = append(pub, payload...)
		_, _ = conn.Write(pub)
		_, ack, err := ReadPacket(br)
		if err != nil || len(ack) < 2 || ack[0] != 0 || ack[1] != 9 {
			return
		}
		got <- "ok"
	}()
	c := &Client{
		Addr: ln.Addr().String(), ClientID: "p_abcd1234", Username: "user", Password: "secret",
		Topic: "/status/1/u/m/us/", Insecure: true,
		OnMessage: func(b []byte) { got <- string(b) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.ServeOnce(ctx) }()
	select {
	case msg := <-got:
		if msg == "ok" {
			t.Fatal("broker finished before the payload")
		}
		if !bytes.Contains([]byte(msg), []byte("ota_state")) {
			t.Fatal(msg)
		}
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("timeout")
	}
	c.Close()
}

func TestSUBACKRefusal(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		_, _, _ = ReadPacket(br)
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		_, _, _ = ReadPacket(br)
		_, _ = conn.Write([]byte{0x90, 0x03, 0x00, 0x01, 0x80})
	}()
	c := &Client{Addr: ln.Addr().String(), ClientID: "p_abcd1234", Username: "user", Password: "secret", Topic: "/status/1/u/m/us/", Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	err = c.ServeOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "suback") {
		t.Fatal(err)
	}
}

func TestSUBACKTruncatedRejected(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		_, _, _ = ReadPacket(br)
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		_, _, _ = ReadPacket(br)
		_, _ = conn.Write([]byte{0x90, 0x02, 0x00, 0x01})
	}()
	c := &Client{Addr: ln.Addr().String(), ClientID: "p_abcd1234", Username: "user", Password: "secret", Topic: "/status/1/u/m/us/", Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	err = c.ServeOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "suback") {
		t.Fatal(err)
	}
}

func TestReadPacketRejectsHugeLength(t *testing.T) {
	n := (1 << 20) + 1
	var buf bytes.Buffer
	buf.WriteByte(0x30)
	for {
		b := byte(n % 128)
		n /= 128
		if n > 0 {
			b |= 0x80
		}
		buf.WriteByte(b)
		if n == 0 {
			break
		}
	}
	_, _, err := ReadPacket(bufio.NewReader(&buf))
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatal(err)
	}
}

func TestPingTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		_, _, _ = ReadPacket(br)
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})
		_, _, _ = ReadPacket(br)
		_, _ = conn.Write([]byte{0x90, 0x03, 0x00, 0x01, 0x00})
		for {
			if _, _, err := ReadPacket(br); err != nil {
				return
			}
		}
	}()
	c := &Client{
		Addr: ln.Addr().String(), ClientID: "p_abcd1234", Username: "user", Password: "secret",
		Topic: "/status/1/u/m/us/", pingGap: 20 * time.Millisecond, pingWait: 40 * time.Millisecond,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = c.ServeOnce(ctx)
	if err == nil || !strings.Contains(err.Error(), "ping timeout") {
		t.Fatal(err)
	}
}

type blockingWriteConn struct {
	net.Conn
	started chan struct{}
	closed  chan struct{}
}

func (b *blockingWriteConn) Write(p []byte) (int, error) {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.closed
	return 0, net.ErrClosed
}

func (b *blockingWriteConn) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func (b *blockingWriteConn) SetWriteDeadline(time.Time) error { return nil }

func TestCloseDoesNotDeadlockDuringBlockedWrite(t *testing.T) {
	conn := &blockingWriteConn{started: make(chan struct{}), closed: make(chan struct{})}
	c := &Client{conn: conn}
	wrote := make(chan error, 1)
	go func() {
		wrote <- c.write(conn, encodePing())
	}()
	<-conn.started
	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked waiting for blocked write")
	}
	select {
	case err := <-wrote:
		if err == nil {
			t.Fatal("expected write error after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked write did not unwind")
	}
}

func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
