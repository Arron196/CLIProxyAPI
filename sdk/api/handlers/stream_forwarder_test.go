package handlers

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
)

type countingFlusher struct {
	count int
}

func (f *countingFlusher) Flush() {
	f.count++
}

func TestForwardStreamFlushesDoneAndBatchesChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/stream", nil)

	data := make(chan []byte, 2)
	data <- []byte("one")
	data <- []byte("two")
	close(data)

	errs := make(chan *interfaces.ErrorMessage)
	close(errs)

	flusher := &countingFlusher{}
	cancelCalls := 0
	handler := &BaseAPIHandler{}
	handler.ForwardStream(c, flusher, func(error) {
		cancelCalls++
	}, data, errs, StreamForwardOptions{
		WriteChunk: func(chunk []byte) {
			_, _ = c.Writer.Write(chunk)
		},
		WriteDone: func() {
			_, _ = c.Writer.Write([]byte("[DONE]"))
		},
	})

	if got := recorder.Body.String(); got != "onetwo[DONE]" {
		t.Fatalf("body = %q, want %q", got, "onetwo[DONE]")
	}
	if flusher.count != 1 {
		t.Fatalf("flush count = %d, want %d", flusher.count, 1)
	}
	if cancelCalls != 1 {
		t.Fatalf("cancel calls = %d, want %d", cancelCalls, 1)
	}
}

func TestForwardStreamFlushesKeepAliveAndTerminalError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("GET", "/stream", nil)

	data := make(chan []byte)
	errs := make(chan *interfaces.ErrorMessage, 1)

	flusher := &countingFlusher{}
	cancelErrCh := make(chan error, 1)
	handler := &BaseAPIHandler{}

	go func() {
		time.Sleep(10 * time.Millisecond)
		errs <- &interfaces.ErrorMessage{Error: errors.New("boom")}
		close(errs)
	}()

	keepAliveInterval := 1 * time.Millisecond
	handler.ForwardStream(c, flusher, func(err error) {
		cancelErrCh <- err
	}, data, errs, StreamForwardOptions{
		KeepAliveInterval: &keepAliveInterval,
		WriteKeepAlive: func() {
			_, _ = c.Writer.Write([]byte(": keep-alive\n\n"))
		},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) {
			_, _ = c.Writer.Write([]byte("error:" + errMsg.Error.Error()))
		},
	})

	if flusher.count < 2 {
		t.Fatalf("flush count = %d, want at least %d", flusher.count, 2)
	}
	if got := recorder.Body.String(); len(got) < len("error:boom") || got[len(got)-len("error:boom"):] != "error:boom" {
		t.Fatalf("body = %q, want terminal error suffix", got)
	}
	if err := <-cancelErrCh; err == nil || err.Error() != "boom" {
		t.Fatalf("cancel err = %v, want boom", err)
	}
}
