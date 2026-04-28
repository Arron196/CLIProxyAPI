package api

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/redisqueue"
	log "github.com/sirupsen/logrus"
)

const (
	respPreAuthTimeout  = 10 * time.Second
	respIdleTimeout     = 2 * time.Minute
	respMaxArrayItems   = 3
	respMaxLineBytes    = 4096
	respMaxBulkBytes    = 4096
	respMaxPopCount     = 1000
	respMaxAuthFailures = 5
)

func isRedisRESPPrefix(prefix byte) bool {
	switch prefix {
	case '*', '$', '+', '-', ':':
		return true
	default:
		return false
	}
}

func (s *Server) handleRedisConnection(conn net.Conn, reader *bufio.Reader) {
	defer func() { _ = conn.Close() }()
	if conn == nil || reader == nil || s == nil || s.mgmt == nil {
		return
	}

	clientIP, localClient := resolveRemoteIP(conn.RemoteAddr())
	authenticated := false
	authFailures := 0
	for s.managementRoutesEnabled.Load() {
		if authenticated {
			_ = conn.SetReadDeadline(time.Now().Add(respIdleTimeout))
		} else {
			_ = conn.SetReadDeadline(time.Now().Add(respPreAuthTimeout))
		}
		args, err := readRESPArray(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Debug("failed reading RESP command")
			}
			return
		}
		if len(args) == 0 {
			writeRESPError(conn, "ERR empty command")
			continue
		}

		command := strings.ToUpper(strings.TrimSpace(args[0]))
		switch command {
		case "AUTH":
			password, err := parseAuthPassword(args)
			if err != nil {
				writeRESPError(conn, err.Error())
				continue
			}
			ok, status, message := s.mgmt.AuthenticateManagementKey(clientIP, localClient, password)
			if !ok {
				authFailures++
				if status == http.StatusUnauthorized {
					writeRESPError(conn, "NOAUTH "+message)
				} else {
					writeRESPError(conn, "ERR "+message)
				}
				if authFailures >= respMaxAuthFailures {
					return
				}
				continue
			}
			authenticated = true
			authFailures = 0
			writeRESPSimpleString(conn, "OK")

		case "LPOP", "RPOP":
			if !authenticated {
				_, _, _ = s.mgmt.AuthenticateManagementKey(clientIP, localClient, "")
				writeRESPRawError(conn, "NOAUTH Authentication required.")
				continue
			}
			count, counted, err := parsePopCount(args)
			if err != nil {
				writeRESPError(conn, err.Error())
				continue
			}
			var items [][]byte
			if command == "RPOP" {
				items = redisqueue.PopNewest(count)
			} else {
				items = redisqueue.PopOldest(count)
			}
			if counted {
				writeRESPArray(conn, items)
				continue
			}
			if len(items) == 0 {
				writeRESPNilBulk(conn)
				continue
			}
			writeRESPBulk(conn, items[0])

		case "PING":
			writeRESPSimpleString(conn, "PONG")

		default:
			writeRESPError(conn, "ERR unknown command '"+command+"'")
		}
	}
}

func resolveRemoteIP(addr net.Addr) (string, bool) {
	if addr == nil {
		return "", false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		host = addr.String()
	}
	ip := net.ParseIP(host)
	local := ip != nil && ip.IsLoopback()
	if ip == nil {
		local = strings.EqualFold(host, "localhost")
	}
	return host, local
}

func parseAuthPassword(args []string) (string, error) {
	switch len(args) {
	case 2:
		return args[1], nil
	case 3:
		return args[2], nil
	default:
		return "", fmt.Errorf("ERR wrong number of arguments for 'AUTH' command")
	}
}

func parsePopCount(args []string) (int, bool, error) {
	switch len(args) {
	case 2:
		return 1, false, nil
	case 3:
		count, err := strconv.Atoi(strings.TrimSpace(args[2]))
		if err != nil || count <= 0 || count > respMaxPopCount {
			return 0, false, fmt.Errorf("ERR value is not an integer or out of range")
		}
		return count, true, nil
	default:
		return 0, false, fmt.Errorf("ERR wrong number of arguments for '%s' command", strings.ToUpper(args[0]))
	}
}

func readRESPArray(reader *bufio.Reader) ([]string, error) {
	line, err := readRESPLine(reader)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("expected RESP array")
	}
	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 0 || count > respMaxArrayItems {
		return nil, fmt.Errorf("invalid RESP array length")
	}
	args := make([]string, 0, count)
	for i := 0; i < count; i++ {
		arg, err := readRESPString(reader)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
	}
	return args, nil
}

func readRESPString(reader *bufio.Reader) (string, error) {
	prefix, err := reader.Peek(1)
	if err != nil {
		return "", err
	}
	switch prefix[0] {
	case '$':
		return readRESPBulkString(reader)
	case '+', '-', ':':
		line, err := readRESPLine(reader)
		if err != nil {
			return "", err
		}
		if len(line) == 0 {
			return "", nil
		}
		return line[1:], nil
	default:
		return "", fmt.Errorf("unsupported RESP string type %q", prefix[0])
	}
}

func readRESPBulkString(reader *bufio.Reader) (string, error) {
	line, err := readRESPLine(reader)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(line, "$") {
		return "", fmt.Errorf("expected RESP bulk string")
	}
	length, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || length < -1 || length > respMaxBulkBytes {
		return "", fmt.Errorf("invalid RESP bulk string length")
	}
	if length == -1 {
		return "", nil
	}
	buf := make([]byte, length+2)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", err
	}
	if string(buf[length:]) != "\r\n" {
		return "", fmt.Errorf("invalid RESP bulk string terminator")
	}
	return string(buf[:length]), nil
}

func readRESPLine(reader *bufio.Reader) (string, error) {
	var builder strings.Builder
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if builder.Len()+len(fragment) > respMaxLineBytes {
				return "", fmt.Errorf("RESP line too long")
			}
			builder.Write(fragment)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			return "", err
		}
		break
	}
	line := builder.String()
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func writeRESPSimpleString(w io.Writer, value string) {
	_, _ = fmt.Fprintf(w, "+%s\r\n", value)
}

func writeRESPRawError(w io.Writer, message string) {
	_, _ = fmt.Fprintf(w, "-%s\r\n", message)
}

func writeRESPError(w io.Writer, message string) {
	if strings.HasPrefix(message, "ERR ") || strings.HasPrefix(message, "NOAUTH ") {
		writeRESPRawError(w, message)
		return
	}
	writeRESPRawError(w, "ERR "+message)
}

func writeRESPNilBulk(w io.Writer) {
	_, _ = io.WriteString(w, "$-1\r\n")
}

func writeRESPBulk(w io.Writer, payload []byte) {
	_, _ = fmt.Fprintf(w, "$%d\r\n", len(payload))
	_, _ = w.Write(payload)
	_, _ = io.WriteString(w, "\r\n")
}

func writeRESPArray(w io.Writer, items [][]byte) {
	_, _ = fmt.Fprintf(w, "*%d\r\n", len(items))
	for _, item := range items {
		writeRESPBulk(w, item)
	}
}
