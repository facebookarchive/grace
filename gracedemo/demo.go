// Command gracedemo implements a demo server showing how to gracefully
// terminate an HTTP server using go.grace.
package main

import (
	"flag"
	"fmt"
	"github.com/daaku/go.grace/gracehttp"
	"net/http"
	"os"
	"time"
)

var (
	address0 = flag.String("a0", ":48567", "Zero address to bind to.")
	address1 = flag.String("a1", ":48568", "First address to bind to.")
	address2 = flag.String("a2", ":48569", "Second address to bind to.")
	now      = time.Now()
)

func main() {
	flag.Parse()
	gracehttp.Serve(
		gracehttp.Handler{*address0, newHandler("Zero  ")},
		gracehttp.Handler{*address1, newHandler("First ")},
		gracehttp.Handler{*address2, newHandler("Second")},
	)
}

func newHandler(name string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sleep/", func(w http.ResponseWriter, r *http.Request) {
		duration, err := time.ParseDuration(r.FormValue("duration"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		time.Sleep(duration)
		fmt.Fprintf(
			w,
			"%s started at %s slept for %d nanoseconds from pid %d.\n",
			name,
			now,
			duration.Nanoseconds(),
			os.Getpid(),
		)
	})
	return mux
}
