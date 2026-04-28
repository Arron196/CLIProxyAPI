package api

import (
	"net"
	"sync"
)

type muxListener struct {
	addr    net.Addr
	connCh  chan net.Conn
	closeCh chan struct{}
	mu      sync.Mutex
	closed  bool
	once    sync.Once
}

func newMuxListener(addr net.Addr, buffer int) *muxListener {
	if buffer <= 0 {
		buffer = 1
	}
	return &muxListener{
		addr:    addr,
		connCh:  make(chan net.Conn, buffer),
		closeCh: make(chan struct{}),
	}
}

func (l *muxListener) Accept() (net.Conn, error) {
	if l == nil {
		return nil, net.ErrClosed
	}
	select {
	case conn := <-l.connCh:
		if conn == nil {
			return nil, net.ErrClosed
		}
		return conn, nil
	case <-l.closeCh:
		return nil, net.ErrClosed
	}
}

func (l *muxListener) Put(conn net.Conn) bool {
	if l == nil || conn == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	select {
	case l.connCh <- conn:
		return true
	default:
		return false
	}
}

func (l *muxListener) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		close(l.closeCh)
	})
	return nil
}

func (l *muxListener) Addr() net.Addr {
	if l == nil {
		return nil
	}
	return l.addr
}
