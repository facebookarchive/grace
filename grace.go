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
	"time"
)

var (
	// This error is returned by Inherits() when we're not inheriting any fds.
	ErrNotInheriting = errors.New("no inherited listeners")

	// This error is returned by Listener.Accept() when Close is in progress.
	ErrAlreadyClosed = errors.New("already closed")

	// Time in the past to trigger immediate deadline.
	timeInPast = time.Now()
)

const (
	// Used to indicate a graceful restart in the new process.
	envCountKey       = "LISTEN_FDS"
	envCountKeyPrefix = envCountKey + "="

	// The error returned by the standard library when the socket is closed.
	errClosed = "use of closed network connection"
)

// A Listener providing a graceful Close process and can be sent
// across processes using the underlying File descriptor.
type Listener interface {
	net.Listener

	// Will return the underlying file representing this Listener.
	File() (f *os.File, err error)
}

type listener struct {
	Listener
	closed      bool
	closedMutex sync.RWMutex
	wg          sync.WaitGroup
}

type deadliner interface {
	SetDeadline(t time.Time) error
}

// Allows for us to notice when the connection is closed.
type conn struct {
	net.Conn
	wg *sync.WaitGroup
}

func (c conn) Close() error {
	defer c.wg.Done()
	return c.Conn.Close()
}

// Wraps an existing File listener to provide a graceful Close() process.
func NewListener(l Listener) Listener {
	return &listener{Listener: l}
}

func (l *listener) Close() error {
	l.closedMutex.Lock()
	l.closed = true
	l.closedMutex.Unlock()

	var err error
	// Init provided sockets dont actually close so we trigger Accept to return
	// by setting the deadline.
	if os.Getppid() == 1 {
		if ld, ok := l.Listener.(deadliner); ok {
			err = ld.SetDeadline(timeInPast)
		} else {
			fmt.Fprintln(os.Stderr, "init activated server did not have SetDeadline")
		}
	} else {
		err = l.Listener.Close()
	}
	l.wg.Wait()
	return err
}

func (l *listener) Accept() (net.Conn, error) {
	// Presume we'll accept and decrement in defer if we don't. If we did this
	// after a successful accept we would have a race condition where we may end
	// up incorrectly shutting down between the time we do a successful accept
	// and the increment.
	var c net.Conn
	l.wg.Add(1)
	defer func() {
		// If we didn't accept, we decrement our presumptuous count above.
		if c == nil {
			l.wg.Done()
		}
	}()

	l.closedMutex.RLock()
	if l.closed {
		l.closedMutex.RUnlock()
		return nil, ErrAlreadyClosed
	}
	l.closedMutex.RUnlock()

	c, err := l.Listener.Accept()
	if err != nil {
		if strings.HasSuffix(err.Error(), errClosed) {
			return nil, ErrAlreadyClosed
		}

		// We use SetDeadline above to trigger Accept to return when we're trying
		// to handoff to a child as part of our restart process. In this scenario
		// we want to treat the timeout the same as a Close.
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			l.closedMutex.RLock()
			if l.closed {
				l.closedMutex.RUnlock()
				return nil, ErrAlreadyClosed
			}
			l.closedMutex.RUnlock()
		}
		return nil, err
	}
	return conn{Conn: c, wg: &l.wg}, nil
}

type Process struct {
}

// Wait for signals to gracefully terminate or restart the process.
func (p *Process) Wait(listeners []Listener) (err error) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGUSR2)
	for {
		sig := <-ch
		switch sig {
		case syscall.SIGTERM:
			signal.Stop(ch)
			var wg sync.WaitGroup
			wg.Add(len(listeners))
			for _, l := range listeners {
				go func(l Listener) {
					defer wg.Done()
					cErr := l.Close()
					if cErr != nil {
						err = cErr
					}
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
}

// Try to inherit listeners from the parent process.
func (p *Process) Inherit() (listeners []Listener, err error) {
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
		l := tmp.(Listener)
		listeners = append(listeners, NewListener(l))
	}
	return
}

// Start the Close process in the parent. This does not wait for the
// parent to close and simply sends it the TERM signal.
func (p *Process) CloseParent() error {
	ppid := os.Getppid()
	if ppid == 1 { // init provided sockets, for example systemd
		return nil
	}
	return syscall.Kill(ppid, syscall.SIGTERM)
}

// Restart the process passing the given listeners to the new process.
func (p *Process) Restart(listeners []Listener) (err error) {
	if len(listeners) == 0 {
		return errors.New("restart must be given listeners.")
	}

	// Extract the fds from the listeners.
	files := make([]*os.File, len(listeners))
	for i, l := range listeners {
		files[i], err = l.File()
		if err != nil {
			return err
		}
		defer files[i].Close()
		syscall.CloseOnExec(int(files[i].Fd()))
	}

	// Use the original binary location. This works with symlinks such that if
	// the file it points to has been changed we will use the updated symlink.
	argv0, err := exec.LookPath(os.Args[0])
	if err != nil {
		return err
	}

	// In order to keep the working directory the same as when we started.
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	// Pass on the environment and replace the old count key with the new one.
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, envCountKeyPrefix) {
			env = append(env, v)
		}
	}
	env = append(env, fmt.Sprintf("%s%d", envCountKeyPrefix, len(listeners)))

	allFiles := append([]*os.File{os.Stdin, os.Stdout, os.Stderr}, files...)
	_, err = os.StartProcess(argv0, os.Args, &os.ProcAttr{
		Dir:   wd,
		Env:   env,
		Files: allFiles,
	})
	return err
}

var defaultProcess = &Process{}

// Wait for signals to gracefully terminate or restart the process.
func Wait(listeners []Listener) (err error) {
	return defaultProcess.Wait(listeners)
}

// Try to inherit listeners from the parent process.
func Inherit() (listeners []Listener, err error) {
	return defaultProcess.Inherit()
}

// Start the Close process in the parent. This does not wait for the
// parent to close and simply sends it the TERM signal.
func CloseParent() error {
	return defaultProcess.CloseParent()
}

// Restart the process passing the given listeners to the new process.
func Restart(listeners []Listener) (err error) {
	return defaultProcess.Restart(listeners)
}
