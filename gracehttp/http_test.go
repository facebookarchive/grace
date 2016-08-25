package gracehttp

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/facebookgo/freeport"
)

func TestServeFallback_IncorrectAddress(t *testing.T) {
	// Prepare a valid port for the test.
	p, err := freeport.Get()
	if err != nil {
		t.Error(err)
	}

	// Allocate a slice of test servers:
	// 1. A valid one;
	// 2. A server with incorrect address.
	ss := []*http.Server{
		{Addr: fmt.Sprintf(":%d", p)},
		{Addr: ":-1"}, // A server with invalid address that is expected to cause an error.
	}

	// Make sure the serveFallback returns an error.
	if err := serveFallback(ss); err == nil {
		t.Errorf("Incorrect input server: a non-nil error expected, got %v.", err)
	}
}

func TestServeFallback(t *testing.T) {
	// Prepare valid ports for the test.
	p1, err := freeport.Get()
	if err != nil {
		t.Error(err)
	}
	p2, err := freeport.Get()
	if err != nil {
		t.Error(err)
	}

	// Allocate a slice of servers with correct addresses.
	ss := []*http.Server{
		{Addr: fmt.Sprintf(":%d", p1), Handler: http.NotFoundHandler(), TLSConfig: &tls.Config{}},
		{Addr: fmt.Sprintf("127.0.0.1:%d", p2), Handler: http.NotFoundHandler()},
	}

	// Run the serveFallback in a separate goroutine.
	go func() { serveFallback(ss) }()
	<-time.Tick(time.Second * 2) // Wait till everything is started.

	// Access the second server.
	res, err := http.Get("http://" + ss[1].Addr)
	if err != nil || res.StatusCode != http.StatusNotFound {
		t.Errorf("Server %s expected to handle requests by returning 404 error. Error: %v.", ss[1].Addr, err)
	}
}
