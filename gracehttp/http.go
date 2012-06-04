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

type Handlers []Handler

var (
	verbose                     = flag.Bool("gracehttp.log", true, "Enable logging.")
	ErrUnexpectedListenersCount = errors.New("unexpected listeners count")
)

// Creates new listeners for all the given addresses.
func (handlers Handlers) newListeners() ([]grace.Listener, error) {
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

// Serve on the given listeners.
func (handlers Handlers) serve(listeners []grace.Listener) error {
	if len(handlers) != len(listeners) {
		return ErrUnexpectedListenersCount
	}
	for i, l := range listeners {
		go func(i int, l net.Listener) {
			err := http.Serve(l, handlers[i].Handler)
			// The underlying Accept() will return grace.ErrAlreadyClosed
			// when a signal to do the same is returned, which we are okay with.
			if err != nil && err != grace.ErrAlreadyClosed {
				log.Fatalf("Failed http.Serve: %s", err)
			}
		}(i, l)
	}
	// TODO errors should be returned not fataled
	return nil
}

// Serve will listen on the given address. It will also wait for a
// SIGUSR2 signal and will restart the server passing the active listener
// to the new process and avoid dropping active connections.
func Serve(givenHandlers ...Handler) {
	handlers := Handlers(givenHandlers)
	listeners, err := grace.Inherit()
	if err == nil {
		if *verbose {
			log.Printf(
				"Graceful handoff of %s with new pid %d and old pid %d.",
				pprintAddr(listeners), os.Getpid(), os.Getppid())
		}
		err = grace.CloseParent()
		if err != nil {
			log.Fatalf("Failed to close parent: %s", err)
		}
	} else if err == grace.ErrNotInheriting {
		listeners, err = handlers.newListeners()
		if err != nil {
			log.Fatal(err)
		}
		if *verbose {
			log.Printf("Serving %s with pid %d.", pprintAddr(listeners), os.Getpid())
		}
	} else {
		log.Fatalf("Failed graceful handoff: %s", err)
	}
	go handlers.serve(listeners)
	err = grace.Wait(listeners)
	if err != nil {
		log.Fatalf("Failed grace.Wait: %s", err)
	}
	if *verbose {
		log.Printf("Exiting pid %d.", os.Getpid())
	}
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
