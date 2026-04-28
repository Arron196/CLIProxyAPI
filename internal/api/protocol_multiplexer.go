package api

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const muxRouteTimeout = 5 * time.Second

func normalizeHTTPServeError(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if strings.Contains(err.Error(), "use of closed network connection") {
		return nil
	}
	return err
}

func normalizeListenerError(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return nil
	}
	if strings.Contains(err.Error(), "use of closed network connection") {
		return nil
	}
	return err
}

func (s *Server) acceptMuxConnections(listener net.Listener, httpListener *muxListener) error {
	if listener == nil || httpListener == nil {
		return net.ErrClosed
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			return normalizeListenerError(err)
		}
		go s.routeMuxConnection(conn, httpListener)
	}
}

func (s *Server) routeMuxConnection(conn net.Conn, httpListener *muxListener) {
	if conn == nil {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(muxRouteTimeout))

	if tlsConn, ok := conn.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			log.WithError(err).Debug("failed TLS handshake")
			_ = conn.Close()
			return
		}
		proto := tlsConn.ConnectionState().NegotiatedProtocol
		if proto != "" && proto != "h2" && proto != "http/1.1" {
			_ = conn.Close()
			return
		}
		if proto == "h2" || proto == "http/1.1" {
			_ = conn.SetDeadline(time.Time{})
			if !httpListener.Put(tlsConn) {
				_ = conn.Close()
			}
			return
		}
		routePlainMuxConnection(s, tlsConn, httpListener)
		return
	}
	routePlainMuxConnection(s, conn, httpListener)
}

func routePlainMuxConnection(s *Server, conn net.Conn, httpListener *muxListener) {
	reader := bufio.NewReader(conn)
	prefix, err := reader.Peek(1)
	if err != nil {
		_ = conn.Close()
		return
	}
	if len(prefix) > 0 && isRedisRESPPrefix(prefix[0]) {
		if s != nil && s.managementRoutesEnabled.Load() {
			_ = conn.SetDeadline(time.Time{})
			s.handleRedisConnection(conn, reader)
			return
		}
		_ = conn.Close()
		return
	}

	buffered := &bufferedConn{Conn: conn, reader: reader}
	_ = conn.SetDeadline(time.Time{})
	if !httpListener.Put(buffered) {
		_ = conn.Close()
	}
}
