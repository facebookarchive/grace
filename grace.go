// Package grace allows for gracefully waiting for a listener to
// finish serving it's active requests.
package grace

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var (
	// This error is returned by Inherits() when we're not inheriting any fds.
	ErrNotInheriting = errors.New("no inherited listeners")

	// This error is returned by Listener.Accept() when Close is in progress.
	ErrAlreadyClosed = errors.New("already closed")
)

const (
	// Used to indicate a graceful restart in the new process.
	envCountKey = "LISTEN_FDS"

	// The error returned by the standard library when the socket is closed.
	errClosed = "use of closed network connection"
)

// A FileListener is a file backed net.Listener.
type FileListener interface {
	net.Listener

	// Will return the underlying file representing this Listener.
	File() (f *os.File, err error)
}

// A Listener providing a graceful Close process and can be sent
// across processes using the underlying File descriptor.
type Listener interface {
	FileListener

	// Will indicate that a Close is requested preventing further Accept. It will
	// also wait for the active connections to be terminated before returning.
	// Note, this won't actually do the close, and is provided as part of the
	// public API for cases where the socket must not be closed (such as systemd
	// activation).
	CloseRequest()
}

type listener struct {
	FileListener
	closed      bool
	closedMutex sync.RWMutex
	wg          sync.WaitGroup
}

// Allows for us to notice when the connection is closed.
type conn struct {
	net.Conn
	wg *sync.WaitGroup
}

func (c conn) Close() error {
	err := c.Conn.Close()
	c.wg.Done()
	return err
}

// Wraps an existing File listener to provide a graceful Close() process.
func NewListener(l FileListener) Listener {
	return &listener{FileListener: l}
}

func (l *listener) CloseRequest() {
	l.closedMutex.Lock()
	l.closed = true
	l.closedMutex.Unlock()
	l.wg.Wait()
}

func (l *listener) Close() error {
	l.CloseRequest()
	return l.FileListener.Close()
}

func (l *listener) Accept() (net.Conn, error) {
	c, err := l.FileListener.Accept()
	if err != nil {
		if strings.HasSuffix(err.Error(), errClosed) {
			return nil, ErrAlreadyClosed
		}
		return nil, err
	}
	l.closedMutex.RLock()
	defer l.closedMutex.RUnlock()
	if l.closed {
		c.Close()
		return nil, ErrAlreadyClosed
	}
	l.wg.Add(1)
	return conn{Conn: c, wg: &l.wg}, nil
}

// Wait for signals to gracefully terminate or restart the process.
func Wait(listeners []Listener) (err error) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGUSR2)
	for {
		sig := <-ch
		switch sig {
		case syscall.SIGTERM:
			var wg sync.WaitGroup
			wg.Add(len(listeners))
			for _, l := range listeners {
				go func(l Listener) {
					if os.Getppid() == 1 { // init provided sockets dont actually close
						l.CloseRequest()
					} else {
						cErr := l.Close()
						if cErr != nil {
							err = cErr
						}
					}
					wg.Done()
				}(l)
			}
			wg.Wait()
			return
		case syscall.SIGUSR2:
			rErr := Restart(listeners)
			if rErr != nil {
				return rErr
			}
		}
	}
	panic("not reached")
}

// Try to inherit listeners from the parent process.
func Inherit() (listeners []Listener, err error) {
	countStr := os.Getenv(envCountKey)
	if countStr == "" {
		return nil, ErrNotInheriting
	}
	count, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, err
	}
	// If we are inheriting, the listeners will begin at fd 3
	for i := 3; i < 3+count; i++ {
		file := os.NewFile(uintptr(i), "listener")
		tmp, err := net.FileListener(file)
		file.Close()
		if err != nil {
			return nil, err
		}
		l := tmp.(*net.TCPListener)
		listeners = append(listeners, NewListener(l))
	}
	return
}

// Start the Close process in the parent. This does not wait for the
// parent to close and simply sends it the TERM signal.
func CloseParent() error {
	ppid := os.Getppid()
	if ppid == 1 { // init provided sockets, for example systemd
		return nil
	}
	return syscall.Kill(ppid, syscall.SIGTERM)
}

// Restart the process passing the given listeners to the new process.
func Restart(listeners []Listener) (err error) {
	if len(listeners) == 0 {
		return errors.New("restart must be given listeners.")
	}
	files := make([]*os.File, len(listeners))
	for i, l := range listeners {
		files[i], err = l.File()
		if err != nil {
			return err
		}
		defer files[i].Close()
		syscall.CloseOnExec(int(files[i].Fd()))
	}
	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	allFiles := append([]*os.File{os.Stdin, os.Stdout, os.Stderr}, files...)
	allFiles = append(allFiles, nil)
	_, err = os.StartProcess(argv0, os.Args, &os.ProcAttr{
		Dir:   wd,
		Env:   append(os.Environ(), fmt.Sprintf("%s=%d", envCountKey, len(files))),
		Files: allFiles,
	})
	return err
}
