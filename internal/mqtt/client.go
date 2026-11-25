package mqtt

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"dreame-dashboard/internal/logx"
)

// Client is a single-topic MQTT 3.1.1 subscriber.
type Client struct {
	Addr       string
	ServerName string
	CAFile     string
	Insecure   bool
	ClientID   string
	Username   string
	Password   string
	Topic      string
	OnMessage  func([]byte)
	OnReady    func()
	Dial       func(context.Context, string, string) (net.Conn, error)
	pingGap    time.Duration
	pingWait   time.Duration

	mu      sync.Mutex
	writeMu sync.Mutex
	conn    net.Conn
}

// Close drops the current TCP session so the caller can reconnect.
func (c *Client) Close() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// ServeOnce dials, connects, subscribes, and reads until the session ends.
func (c *Client) ServeOnce(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
	}()
	doneShake := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-doneShake:
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := conn.Write(encodeConnect(c.ClientID, c.Username, c.Password)); err != nil {
		close(doneShake)
		return err
	}
	br := bufio.NewReader(conn)
	first, body, err := ReadPacket(br)
	if err != nil {
		close(doneShake)
		return err
	}
	if first != 0x20 || len(body) < 2 || body[1] != 0 {
		close(doneShake)
		return errors.New("connack")
	}
	if _, err := conn.Write(encodeSubscribe(1, c.Topic)); err != nil {
		close(doneShake)
		return err
	}
	first, body, err = ReadPacket(br)
	if err != nil {
		close(doneShake)
		return err
	}
	close(doneShake)
	if first&0xF0 != 0x90 || len(body) < 3 || body[2] == 0x80 {
		return errors.New("suback")
	}
	_ = conn.SetWriteDeadline(time.Time{})
	logx.Info("mqtt", "mqtt connected", "topic", c.Topic)
	if c.OnReady != nil {
		c.OnReady()
	}
	readWait := 100 * time.Second
	_ = conn.SetReadDeadline(time.Now().Add(readWait))
	errc := make(chan error, 1)
	pong := make(chan struct{}, 1)
	go func() {
		for {
			first, body, err := ReadPacket(br)
			if err != nil {
				errc <- err
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(readWait))
			switch first & 0xF0 {
			case 0x30:
				_, payload, qos, id, err := DecodePublish(first, body)
				if err != nil {
					errc <- err
					return
				}
				if qos > 0 {
					if err := c.write(conn, encodePubAck(id)); err != nil {
						errc <- err
						return
					}
				}
				if c.OnMessage != nil && len(payload) > 0 {
					c.OnMessage(payload)
				}
			case 0xD0:
				select {
				case pong <- struct{}{}:
				default:
				}
			}
		}
	}()
	every := 40 * time.Second
	if c.pingGap > 0 {
		every = c.pingGap
	}
	wait := 15 * time.Second
	if c.pingWait > 0 {
		wait = c.pingWait
	}
	ping := time.NewTicker(every)
	defer ping.Stop()
	pending := time.NewTimer(time.Hour)
	defer pending.Stop()
	awaiting := false
	for {
		select {
		case <-ctx.Done():
			_ = c.write(conn, encodeDisconnect())
			return nil
		case err := <-errc:
			return err
		case <-pong:
			awaiting = false
		case <-pending.C:
			if awaiting {
				_ = conn.Close()
				return errors.New("ping timeout")
			}
		case <-ping.C:
			if awaiting {
				_ = conn.Close()
				return errors.New("ping timeout")
			}
			if err := c.write(conn, encodePing()); err != nil {
				return err
			}
			awaiting = true
			if !pending.Stop() {
				select {
				case <-pending.C:
				default:
				}
			}
			pending.Reset(wait)
		}
	}
}

func joinIPs(ips []net.IP) string {
	parts := make([]string, len(ips))
	for i, ip := range ips {
		parts[i] = ip.String()
	}
	return strings.Join(parts, ",")
}

func (c *Client) write(conn net.Conn, b []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := conn.Write(b)
	return err
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	if c.Dial != nil {
		return c.Dial(ctx, "tcp", c.Addr)
	}
	name := c.ServerName
	if name == "" {
		name = c.Addr
		if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name, "]") {
			name = name[:i]
		}
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name, InsecureSkipVerify: c.Insecure}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("mqtt ca")
		}
		cfg.RootCAs = pool
	}
	if c.Insecure {
		logx.Warn("mqtt", "tls verify disabled")
	}
	conn, err := (&tls.Dialer{Config: cfg}).DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return nil, err
	}
	if tc, ok := conn.(*tls.Conn); ok {
		st := tc.ConnectionState()
		for i, cert := range st.PeerCertificates {
			logx.Info("mqtt", "tls peer", "i", strconv.Itoa(i), "issuer", cert.Issuer.String(), "not_before", cert.NotBefore.UTC().Format(time.RFC3339), "dns", strings.Join(cert.DNSNames, ","), "ips", joinIPs(cert.IPAddresses))
		}
	}
	return conn, nil
}
