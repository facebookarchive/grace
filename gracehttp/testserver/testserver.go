// Command testserver implements a test case.
package main

import (
	"encoding/json"
	"flag"
	"github.com/nshah/go.grace/gracehttp"
	"log"
	"net/http"
	"os"
	"time"
)

type response struct {
	Sleep time.Duration
	Pid   int
}

var (
	address0 = flag.String("a0", ":48567", "Zero address to bind to.")
	address1 = flag.String("a1", ":48568", "First address to bind to.")
	address2 = flag.String("a2", ":48569", "Second address to bind to.")
)

func main() {
	flag.Parse()
	err := json.NewEncoder(os.Stderr).Encode(&response{Pid: os.Getpid()})
	if err != nil {
		log.Fatalf("Error writing startup json: %s", err)
	}
	gracehttp.Serve(
		gracehttp.Handler{*address0, newHandler()},
		gracehttp.Handler{*address1, newHandler()},
		gracehttp.Handler{*address2, newHandler()},
	)
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sleep/", func(w http.ResponseWriter, r *http.Request) {
		duration, err := time.ParseDuration(r.FormValue("duration"))
		if err != nil {
			http.Error(w, err.Error(), 400)
		}
		time.Sleep(duration)
		err = json.NewEncoder(w).Encode(&response{
			Sleep: duration,
			Pid:   os.Getpid(),
		})
		if err != nil {
			log.Fatalf("Error encoding json: %s", err)
		}
	})
	return mux
}
