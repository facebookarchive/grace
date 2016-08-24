// Package gracehttp provides easy to use graceful restart
// functionality for HTTP server.
package gracehttp

import (
	"flag"
	"log"
	"net/http"
	"os"
)

var (
	verbose = flag.Bool("gracehttp.log", true, "Enable logging.")
)

// serveFallback is a wrapper around the standard ServerAndListen
// and ServeAndListenTLS methods.
// It is used instead of gracehttp.serve on Windows.
func serveFallback(servers ...*http.Server) (err error) {
	for i := 0; i < len(servers); i++ {
		go func(s *http.Server) {
			if *verbose {
				log.Printf("Serving %s with pid %d.", s.Addr, os.Getpid())
			}

			// Detecting whether TLS connection should be used
			// rather than a plain HTTP.
			switch s.TLSConfig {
			case nil:
				err = s.ListenAndServe()
			default:
				// Using empty arguments as we expect
				// the values from TLSConfig will be used.
				// NB: Go 1.5 is required (see golang issue #8599).
				err = s.ListenAndServeTLS("", "")
			}

			defer func() {
				if *verbose {
					log.Printf("Exiting pid %d.", os.Getpid())
				}
			}()
		}(servers[i])

		// If the server goroutine failed, do not proceed to
		// make sure the behaviour of serveFallback is similar
		// to the one of *nix serve.
		if err != nil {
			return
		}
	}
	return
}
