package api

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/redisqueue"
	"golang.org/x/crypto/bcrypt"
)

type loopbackConn struct {
	net.Conn
}

func (c loopbackConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 45678}
}

func TestRedisQueueProtocolAuthAndPop(t *testing.T) {
	server := newTestServer(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to hash secret: %v", err)
	}
	server.cfg.RemoteManagement.SecretKey = string(hash)
	server.managementRoutesEnabled.Store(true)
	server.mgmt.SetConfig(server.cfg)
	redisqueue.SetEnabled(true)
	t.Cleanup(func() { redisqueue.SetEnabled(false) })
	redisqueue.Enqueue([]byte(`{"tokens":{"total_tokens":42}}`))

	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		wrapped := loopbackConn{Conn: serverSide}
		server.handleRedisConnection(wrapped, bufio.NewReader(wrapped))
	}()
	defer func() {
		_ = clientSide.Close()
		<-done
	}()

	reader := bufio.NewReader(clientSide)
	writeRESPCommand(t, clientSide, "LPOP", "usage")
	if line := readRESPLineForTest(t, reader); line != "-NOAUTH Authentication required." {
		t.Fatalf("unauthenticated response = %q", line)
	}

	writeRESPCommand(t, clientSide, "AUTH", "secret")
	if line := readRESPLineForTest(t, reader); line != "+OK" {
		t.Fatalf("auth response = %q", line)
	}

	writeRESPCommand(t, clientSide, "LPOP", "usage")
	lengthLine := readRESPLineForTest(t, reader)
	if !strings.HasPrefix(lengthLine, "$") {
		t.Fatalf("bulk length line = %q", lengthLine)
	}
	payload := readRESPLineForTest(t, reader)
	if !strings.Contains(payload, `"total_tokens":42`) {
		t.Fatalf("payload = %q, want total_tokens", payload)
	}

	writeRESPCommand(t, clientSide, "LPOP", "usage")
	if line := readRESPLineForTest(t, reader); line != "$-1" {
		t.Fatalf("empty pop response = %q, want $-1", line)
	}
}

func TestRedisQueueProtocolAcceptsLocalPasswordWithoutRemoteSecret(t *testing.T) {
	server := newTestServer(t)
	server.mgmt.SetLocalPassword("local-secret")
	server.managementRoutesEnabled.Store(true)

	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		wrapped := loopbackConn{Conn: serverSide}
		server.handleRedisConnection(wrapped, bufio.NewReader(wrapped))
	}()
	defer func() {
		_ = clientSide.Close()
		<-done
	}()

	reader := bufio.NewReader(clientSide)
	writeRESPCommand(t, clientSide, "AUTH", "local-secret")
	if line := readRESPLineForTest(t, reader); line != "+OK" {
		t.Fatalf("auth response = %q", line)
	}
}

func TestRedisQueueProtocolRPopUsesNewest(t *testing.T) {
	server := newTestServer(t)
	server.mgmt.SetLocalPassword("local-secret")
	server.managementRoutesEnabled.Store(true)
	redisqueue.SetEnabled(true)
	t.Cleanup(func() { redisqueue.SetEnabled(false) })
	redisqueue.Enqueue([]byte("oldest"))
	redisqueue.Enqueue([]byte("newest"))

	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		wrapped := loopbackConn{Conn: serverSide}
		server.handleRedisConnection(wrapped, bufio.NewReader(wrapped))
	}()
	defer func() {
		_ = clientSide.Close()
		<-done
	}()

	reader := bufio.NewReader(clientSide)
	writeRESPCommand(t, clientSide, "AUTH", "local-secret")
	if line := readRESPLineForTest(t, reader); line != "+OK" {
		t.Fatalf("auth response = %q", line)
	}
	writeRESPCommand(t, clientSide, "RPOP", "usage")
	_ = readRESPLineForTest(t, reader)
	if payload := readRESPLineForTest(t, reader); payload != "newest" {
		t.Fatalf("RPOP payload = %q, want newest", payload)
	}
	writeRESPCommand(t, clientSide, "LPOP", "usage")
	_ = readRESPLineForTest(t, reader)
	if payload := readRESPLineForTest(t, reader); payload != "oldest" {
		t.Fatalf("LPOP payload = %q, want oldest", payload)
	}
}

func TestRedisQueueProtocolRejectsOversizedBulkAndPopCount(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("$4097\r\n"))
	if _, err := readRESPBulkString(reader); err == nil {
		t.Fatal("readRESPBulkString oversized error = nil, want error")
	}
	reader = bufio.NewReader(strings.NewReader(strings.Repeat("x", respMaxLineBytes+1) + "\n"))
	if _, err := readRESPLine(reader); err == nil {
		t.Fatal("readRESPLine oversized error = nil, want error")
	}
	if _, _, err := parsePopCount([]string{"LPOP", "usage", fmt.Sprint(respMaxPopCount + 1)}); err == nil {
		t.Fatal("parsePopCount oversized error = nil, want error")
	}
}

func TestRedisQueueProtocolUnauthenticatedPopCountsTowardRemoteBan(t *testing.T) {
	server := newTestServer(t)
	server.cfg.RemoteManagement.AllowRemote = true
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to hash secret: %v", err)
	}
	server.cfg.RemoteManagement.SecretKey = string(hash)
	server.managementRoutesEnabled.Store(true)
	server.mgmt.SetConfig(server.cfg)

	for i := 0; i < 5; i++ {
		ok, status, _ := server.mgmt.AuthenticateManagementKey("203.0.113.25", false, "")
		if ok || status == 0 {
			t.Fatalf("failed auth %d = ok %v status %d", i, ok, status)
		}
	}
	ok, status, message := server.mgmt.AuthenticateManagementKey("203.0.113.25", false, "secret")
	if ok || status != 403 || !strings.Contains(message, "IP banned") {
		t.Fatalf("post-ban auth = ok %v status %d message %q, want banned", ok, status, message)
	}
}

func TestRouteMuxConnectionRoutesRESPWhenManagementEnabled(t *testing.T) {
	server := newTestServer(t)
	server.mgmt.SetLocalPassword("local-secret")
	server.managementRoutesEnabled.Store(true)
	redisqueue.SetEnabled(true)
	t.Cleanup(func() { redisqueue.SetEnabled(false) })
	redisqueue.Enqueue([]byte("payload"))

	httpListener := newMuxListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}, 1)
	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.routeMuxConnection(loopbackConn{Conn: serverSide}, httpListener)
	}()
	defer func() {
		_ = clientSide.Close()
		<-done
		_ = httpListener.Close()
	}()

	reader := bufio.NewReader(clientSide)
	writeRESPCommand(t, clientSide, "AUTH", "local-secret")
	if line := readRESPLineForTest(t, reader); line != "+OK" {
		t.Fatalf("auth response = %q", line)
	}
	writeRESPCommand(t, clientSide, "LPOP", "usage")
	_ = readRESPLineForTest(t, reader)
	if payload := readRESPLineForTest(t, reader); payload != "payload" {
		t.Fatalf("payload = %q, want payload", payload)
	}
}

func TestRouteMuxConnectionRoutesHTTPToListener(t *testing.T) {
	server := newTestServer(t)
	server.managementRoutesEnabled.Store(true)
	httpListener := newMuxListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}, 1)
	serverSide, clientSide := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.routeMuxConnection(loopbackConn{Conn: serverSide}, httpListener)
	}()
	defer func() {
		_ = clientSide.Close()
		<-done
		_ = httpListener.Close()
	}()

	if _, err := io.WriteString(clientSide, "GET /healthz HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatalf("failed to write HTTP request: %v", err)
	}
	conn, err := httpListener.Accept()
	if err != nil {
		t.Fatalf("http listener accept error: %v", err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("failed to read routed HTTP bytes: %v", err)
	}
	if string(buf) != "GET" {
		t.Fatalf("routed bytes = %q, want GET", string(buf))
	}
}

func writeRESPCommand(t *testing.T, conn net.Conn, args ...string) {
	t.Helper()
	var builder strings.Builder
	fmt.Fprintf(&builder, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&builder, "$%d\r\n%s\r\n", len(arg), arg)
	}
	if _, err := conn.Write([]byte(builder.String())); err != nil {
		t.Fatalf("failed to write RESP command: %v", err)
	}
}

func readRESPLineForTest(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read RESP line: %v", err)
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line
}
