package supervisor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// ErrCtlUnavailable means no running daemon is listening on ctl.sock.
var ErrCtlUnavailable = errors.New("ctl socket unavailable")

// CtlHandler runs a one-line command from the local socket.
type CtlHandler func(verb string) (string, error)

// ServeCtl listens on a unix socket for poll-now and verify-map.
func ServeCtl(ctx context.Context, path string, fn CtlHandler) error {
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveCtl(conn, fn)
	}
}

func serveCtl(conn net.Conn, fn CtlHandler) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	verb := strings.TrimSpace(line)
	msg, err := fn(verb)
	if err != nil {
		fmt.Fprintf(conn, "error=%s\n", err.Error())
		return
	}
	if msg == "" {
		msg = "ok"
	}
	fmt.Fprintf(conn, "%s\n", msg)
}

// CallCtl dials the socket and returns the response through EOF.
func CallCtl(path, verb string) (string, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrCtlUnavailable, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	if _, err := fmt.Fprintf(conn, "%s\n", verb); err != nil {
		return "", err
	}
	body, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(body))
	if strings.HasPrefix(line, "error=") {
		return "", fmt.Errorf("%s", strings.TrimPrefix(strings.TrimSpace(strings.SplitN(line, "\n", 2)[0]), "error="))
	}
	return line, nil
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "."
	}
	return path[:i]
}
