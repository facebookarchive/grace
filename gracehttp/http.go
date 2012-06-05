// Package gracehttp provides easy to use graceful restart
// functionality for HTTP server.
package gracehttp

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"github.com/nshah/go.grace"
	"log"
	"net"
	"net/http"
	"os"
)

type Handler struct {
	Addr    string
	Handler http.Handler
}

type handlersSlice []Handler

var (
	verbose           = flag.Bool("gracehttp.log", true, "Enable logging.")
	errListenersCount = errors.New("unexpected listeners count")
)

// Creates new listeners for all the given addresses.
func (handlers handlersSlice) newListeners() ([]grace.Listener, error) {
	listeners := make([]grace.Listener, len(handlers))
	for index, pair := range handlers {
		addr, err := net.ResolveTCPAddr("tcp", pair.Addr)
		if err != nil {
			return nil, fmt.Errorf(
				"Failed net.ResolveTCPAddr for %s: %s", pair.Addr, err)
		}
		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("Failed net.ListenTCP for %s: %s", pair.Addr, err)
		}
		listeners[index] = grace.NewListener(l)
	}
	return listeners, nil
}

// Serve on the given listeners and wait for signals.
func (handlers handlersSlice) serveWait(listeners []grace.Listener) error {
	if len(handlers) != len(listeners) {
		return errListenersCount
	}
	errch := make(chan error, len(listeners)+1) // listeners + grace.Wait
	for i, l := range listeners {
		go func(i int, l net.Listener) {
			err := http.Serve(l, handlers[i].Handler)
			// The underlying Accept() will return grace.ErrAlreadyClosed
			// when a signal to do the same is returned, which we are okay with.
			if err != nil && err != grace.ErrAlreadyClosed {
				errch <- fmt.Errorf("Failed http.Serve: %s", err)
			}
		}(i, l)
	}
	go func() {
		err := grace.Wait(listeners)
		if err != nil {
			errch <- fmt.Errorf("Failed grace.Wait: %s", err)
		} else {
			errch <- nil
		}
	}()
	return <-errch
}

// Serve will serve the given pairs of addresses and listeners and
// will monitor for signals allowing for graceful termination (SIGTERM)
// or restart (SIGUSR2).
func Serve(givenHandlers ...Handler) error {
	handlers := handlersSlice(givenHandlers)
	listeners, err := grace.Inherit()
	if err == nil {
		err = grace.CloseParent()
		if err != nil {
			return fmt.Errorf("Failed to close parent: %s", err)
		}
		if *verbose {
			log.Printf(
				"Graceful handoff of %s with new pid %d and old pid %d.",
				pprintAddr(listeners), os.Getpid(), os.Getppid())
		}
	} else if err == grace.ErrNotInheriting {
		listeners, err = handlers.newListeners()
		if err != nil {
			return err
		}
		if *verbose {
			log.Printf("Serving %s with pid %d.", pprintAddr(listeners), os.Getpid())
		}
	} else {
		return fmt.Errorf("Failed graceful handoff: %s", err)
	}
	err = handlers.serveWait(listeners)
	if err != nil {
		return err
	}
	if *verbose {
		return fmt.Errorf("Exiting pid %d.", os.Getpid())
	}
	return nil
}

// Used for pretty printing addresses.
func pprintAddr(listeners []grace.Listener) []byte {
	out := bytes.NewBuffer(nil)
	for i, l := range listeners {
		if i != 0 {
			fmt.Fprint(out, ", ")
		}
		fmt.Fprint(out, l.Addr())
	}
	return out.Bytes()
}
