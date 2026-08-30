package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type gracefulHomeDispatcherTestDouble struct {
	strict   bool
	graceful bool
}

func (d gracefulHomeDispatcherTestDouble) HeartbeatOK() bool {
	return d.strict
}

func (d gracefulHomeDispatcherTestDouble) HeartbeatOKWithin(time.Duration) bool {
	return d.graceful
}

func (gracefulHomeDispatcherTestDouble) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return nil, nil
}

func TestHomeDispatcherHeartbeatOKUsesGraceWindowWhenSupported(t *testing.T) {
	if !homeDispatcherHeartbeatOK(gracefulHomeDispatcherTestDouble{graceful: true}) {
		t.Fatal("homeDispatcherHeartbeatOK() rejected a dispatcher inside grace window")
	}
	if homeDispatcherHeartbeatOK(gracefulHomeDispatcherTestDouble{}) {
		t.Fatal("homeDispatcherHeartbeatOK() accepted an unavailable dispatcher")
	}
}

type strictHomeDispatcherTestDouble struct{}

func (strictHomeDispatcherTestDouble) HeartbeatOK() bool { return true }

func (strictHomeDispatcherTestDouble) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return nil, nil
}

func TestHomeDispatcherHeartbeatOKKeepsLegacyDispatcherContract(t *testing.T) {
	if !homeDispatcherHeartbeatOK(strictHomeDispatcherTestDouble{}) {
		t.Fatal("homeDispatcherHeartbeatOK() rejected a legacy healthy dispatcher")
	}
}
