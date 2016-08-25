// Package gracehttp provides easy to use graceful restart
// functionality for HTTP server.
package gracehttp

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
)

const (
	servingWithPID = "Serving %s with pid %d"
)

var (
	verbose = flag.Bool("gracehttp.log", true, "Enable logging.")
)

// serveFallback is a wrapper around the standard Serve function.
// It is used instead of gracehttp.Serve on Windows
// as the latter method is *nix specific.
func serveFallback(servers []*http.Server) error {
	// Allocate a listener for every of the
	// input servers.
	ls, err := listeners(servers, net.Listen)
	if err != nil {
		return err
	}

	if *verbose {
		log.Printf(servingWithPID, pprintAddr(ls), os.Getpid())
	}

	// Try to start serving every of the servers received
	// as input arguments.
	errc := make(chan error, 1)
	for i := 0; i < len(servers); i++ {
		go func(s *http.Server, l net.Listener) {
			// Start serving the current server using appropriate listener.
			errc <- s.Serve(l)
		}(servers[i], ls[i])
	}

	// Return an error as soon as one of the server goroutines fails.
	return <-errc
}

// listenFn is a function type. Either net.Listen or gracenet.Net.Listen
// satisfy the signature.
type listenFn func(string, string) (net.Listener, error)

// listeners gets a slice of servers and returns a number
// of allocated listener structures for every of them.
// An error is returned if some of the listeners cannot be created.
func listeners(ss []*http.Server, fn listenFn) ([]net.Listener, error) {
	ls := []net.Listener{}
	for _, s := range ss {
		// TODO: default addresses.
		l, err := fn("tcp", s.Addr)
		if err != nil {
			return nil, err
		}
		if s.TLSConfig != nil {
			l = tls.NewListener(l, s.TLSConfig)
		}
		ls = append(ls, l)
	}
	return ls, nil
}

// pprintAddr is used for pretty printing addresses.
func pprintAddr(listeners []net.Listener) []byte {
	var out bytes.Buffer
	for i, l := range listeners {
		if i != 0 {
			fmt.Fprint(&out, ", ")
		}
		fmt.Fprint(&out, l.Addr())
	}
	return out.Bytes()
}
