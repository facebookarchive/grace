// Command testserver implements a test case.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/daaku/go.grace/gracehttp"
)

type response struct {
	Sleep time.Duration
	Pid   int
}

func wait(wg *sync.WaitGroup, addr string) {
	defer wg.Done()
	url := fmt.Sprintf("http://%s/sleep/?duration=0", addr)
	for {
		if _, err := http.Get(url); err == nil {
			return
		}
	}
}

func main() {
	var addrs [3]string
	flag.StringVar(&addrs[0], "a0", ":48560", "Zero address to bind to.")
	flag.StringVar(&addrs[1], "a1", ":48561", "First address to bind to.")
	flag.StringVar(&addrs[2], "a2", ":48562", "Second address to bind to.")
	flag.Parse()

	err := flag.Set("gracehttp.log", "false")
	if err != nil {
		log.Fatalf("Error setting gracehttp.log: %s", err)
	}

	// print json to stderr once we can successfully connect to all three
	// addresses. the ensures we only print the line once we're ready to serve.
	go func() {
		var wg sync.WaitGroup
		wg.Add(len(addrs))
		for _, addr := range addrs {
			go wait(&wg, addr)
		}
		wg.Wait()

		err = json.NewEncoder(os.Stderr).Encode(&response{Pid: os.Getpid()})
		if err != nil {
			log.Fatalf("Error writing startup json: %s", err)
		}
	}()

	err = gracehttp.Serve(
		&http.Server{Addr: addrs[0], Handler: newHandler()},
		&http.Server{Addr: addrs[1], Handler: newHandler()},
		&http.Server{Addr: addrs[2], Handler: newHandler()},
	)
	if err != nil {
		log.Fatalf("Error in gracehttp.Serve: %s", err)
	}
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
