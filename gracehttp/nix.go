// +build !windows

package gracehttp

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/facebookgo/grace/gracenet"
	"github.com/facebookgo/httpdown"
)

var (
	didInherit = os.Getenv("LISTEN_FDS") != ""
	ppid       = os.Getppid()
)

// An app contains one or more servers and associated configuration.
type app struct {
	servers   []*http.Server
	http      *httpdown.HTTP
	net       *gracenet.Net
	listeners []net.Listener
	sds       []httpdown.Server
	errors    chan error
}

func newApp(servers []*http.Server) *app {
	return &app{
		servers:   servers,
		http:      &httpdown.HTTP{},
		net:       &gracenet.Net{},
		listeners: make([]net.Listener, 0, len(servers)),
		sds:       make([]httpdown.Server, 0, len(servers)),

		// 2x num servers for possible Close or Stop errors + 1 for possible
		// StartProcess error.
		errors: make(chan error, 1+(len(servers)*2)),
	}
}

func (a *app) listen() (err error) {
	a.listeners, err = listeners(a.servers, a.net.Listen)
	return
}

func (a *app) serve() {
	for i, s := range a.servers {
		a.sds = append(a.sds, a.http.Serve(s, a.listeners[i]))
	}
}

func (a *app) wait() {
	var wg sync.WaitGroup
	wg.Add(len(a.sds) * 2) // Wait & Stop
	go a.signalHandler(&wg)
	for _, s := range a.sds {
		go func(s httpdown.Server) {
			defer wg.Done()
			if err := s.Wait(); err != nil {
				a.errors <- err
			}
		}(s)
	}
	wg.Wait()
}

func (a *app) term(wg *sync.WaitGroup) {
	for _, s := range a.sds {
		go func(s httpdown.Server) {
			defer wg.Done()
			if err := s.Stop(); err != nil {
				a.errors <- err
			}
		}(s)
	}
}

func (a *app) signalHandler(wg *sync.WaitGroup) {
	ch := make(chan os.Signal, 10)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGUSR2)
	for {
		sig := <-ch
		switch sig {
		case syscall.SIGTERM:
			// this ensures a subsequent TERM will trigger standard go behaviour of
			// terminating.
			signal.Stop(ch)
			a.term(wg)
			return
		case syscall.SIGUSR2:
			// we only return here if there's an error, otherwise the new process
			// will send us a TERM when it's ready to trigger the actual shutdown.
			if _, err := a.net.StartProcess(); err != nil {
				a.errors <- err
			}
		}
	}
}

// Serve will serve the given http.Servers and will monitor for signals
// allowing for graceful termination (SIGTERM) or restart (SIGUSR2).
func Serve(servers ...*http.Server) error {
	a := newApp(servers)

	// Acquire Listeners
	if err := a.listen(); err != nil {
		return err
	}

	// Some useful logging.
	if *verbose {
		if didInherit {
			if ppid == 1 {
				log.Printf("Listening on init activated %s", pprintAddr(a.listeners))
			} else {
				const msg = "Graceful handoff of %s with new pid %d and old pid %d"
				log.Printf(msg, pprintAddr(a.listeners), os.Getpid(), ppid)
			}
		} else {
			log.Printf(servingWithPID, pprintAddr(a.listeners), os.Getpid())
		}
	}

	// Start serving.
	a.serve()

	// Close the parent if we inherited and it wasn't init that started us.
	if didInherit && ppid != 1 {
		if err := syscall.Kill(ppid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to close parent: %s", err)
		}
	}

	waitdone := make(chan struct{})
	go func() {
		defer close(waitdone)
		a.wait()
	}()

	select {
	case err := <-a.errors:
		if err == nil {
			panic("unexpected nil error")
		}
		return err
	case <-waitdone:
		if *verbose {
			log.Printf(exitingPID, os.Getpid())
		}
		return nil
	}
}
