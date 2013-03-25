package gracehttp_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/daaku/go.freeport"
	"github.com/daaku/go.tool"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	// The amount of time we give a process to become ready.
	processWait = time.Second * 2

	// The amount of time for the long HTTP request. This should be
	// bigger than the value above.
	slowHttpWait = time.Second * 4

	// Debug logging.
	debugLog = false
)

func debug(format string, a ...interface{}) {
	if debugLog {
		println(fmt.Sprintf(format, a...))
	}
}

// The response from the test server.
type response struct {
	Sleep time.Duration
	Pid   int
}

// State for the test run.
type harness struct {
	T                *testing.T     // The test instance.
	ImportPath       string         // The import path for the server command.
	ExeName          string         // The temp binary from the build.
	Addr             []string       // The addresses for the http servers.
	Process          []*os.Process  // The server commands, oldest to newest.
	RequestWaitGroup sync.WaitGroup // The wait group for the HTTP requests.
	newProcess       chan bool      // A bool is sent on restart.
}

// Find 3 free ports and setup addresses.
func (h *harness) SetupAddr() {
	for i := 3; i > 0; i-- {
		port, err := freeport.Get()
		if err != nil {
			h.T.Fatalf("Failed to find a free port: %s", err)
		}
		h.Addr = append(h.Addr, fmt.Sprintf("127.0.0.1:%d", port))
	}
}

// Builds the command line arguments.
func (h *harness) Args() []string {
	if h.Addr == nil {
		h.SetupAddr()
	}
	return []string{
		"-gracehttp.log=false",
		"-a0", h.Addr[0],
		"-a1", h.Addr[1],
		"-a2", h.Addr[2],
	}
}

// Builds the command.
func (h *harness) Build() {
	basename := filepath.Base(h.ImportPath)
	tempFile, err := ioutil.TempFile("", basename+"-")
	if err != nil {
		h.T.Fatalf("Error creating temp file: %s", err)
	}
	h.ExeName = tempFile.Name()
	_ = os.Remove(h.ExeName) // the build tool will create this
	options := tool.Options{
		ImportPaths: []string{h.ImportPath},
		Output:      h.ExeName,
	}
	_, err = options.Command("build")
	if err != nil {
		h.T.Fatal(err)
	}
}

// Start a fresh server and wait for pid updates on restart.
func (h *harness) Start() {
	if h.newProcess == nil {
		h.newProcess = make(chan bool)
	}
	cmd := exec.Command(h.ExeName, h.Args()...)
	stderr, err := cmd.StderrPipe()
	go func() {
		reader := bufio.NewReader(stderr)
		for {
			line, isPrefix, err := reader.ReadLine()
			if err != nil {
				h.T.Fatalf("Failed to read line from server process: %s", err)
			}
			if isPrefix {
				h.T.Fatalf("Deal with isPrefix for line: %s", line)
			}
			res := &response{}
			err = json.Unmarshal([]byte(line), res)
			if err != nil {
				h.T.Fatalf("Could not parse json from stderr %s: %s", line, err)
			}
			process, err := os.FindProcess(res.Pid)
			if err != nil {
				h.T.Fatalf("Could not find process with pid: %d", res.Pid)
			}
			h.Process = append(h.Process, process)
			h.newProcess <- true
		}
	}()
	err = cmd.Start()
	if err != nil {
		h.T.Fatalf("Failed to start command: %s", err)
	}
	h.Process = append(h.Process, cmd.Process)
	<-h.newProcess
	time.Sleep(processWait)
}

// Restart the most recent server.
func (h *harness) Restart() {
	err := h.MostRecentProcess().Signal(syscall.SIGUSR2)
	if err != nil {
		h.T.Fatalf("Failed to send SIGUSR2 and restart process: %s", err)
	}
	<-h.newProcess
	time.Sleep(processWait)
}

// Graceful termination of the most recent server.
func (h *harness) Stop() {
	err := h.MostRecentProcess().Signal(syscall.SIGTERM)
	if err != nil {
		h.T.Fatalf("Failed to send SIGTERM and stop process: %s", err)
	}
}

// Returns the most recent server process.
func (h *harness) MostRecentProcess() *os.Process {
	l := len(h.Process)
	if l == 0 {
		h.T.Fatalf("Most recent command requested before command was created.")
	}
	return h.Process[l-1]
}

// Remove the built executable.
func (h *harness) RemoveExe() {
	err := os.Remove(h.ExeName)
	if err != nil {
		h.T.Fatalf("Failed to RemoveExe: %s", err)
	}
}

// Helper for sending a single request.
func (h *harness) SendOne(duration time.Duration, addr string, pid int) {
	debug("Send One pid=%d duration=%s", pid, duration)
	client := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	url := fmt.Sprintf("http://%s/sleep/?duration=%s", addr, duration.String())
	r, err := client.Get(url)
	if err != nil {
		h.T.Fatalf("Failed request to %s: %s", url, err)
	}
	defer r.Body.Close()
	res := &response{}
	err = json.NewDecoder(r.Body).Decode(res)
	if err != nil {
		h.T.Fatalf("Failed to ready decode json response body: %s", err)
	}
	if pid != res.Pid {
		h.T.Fatalf("Didn't get expected pid %d instead got %d", pid, res.Pid)
	}
	debug("Request Done pid=%d duration=%s", pid, duration)
	h.RequestWaitGroup.Done()
}

// Send test HTTP request.
func (h *harness) SendRequest() {
	pid := h.MostRecentProcess().Pid
	for _, addr := range h.Addr {
		debug("Added 2 Requests")
		h.RequestWaitGroup.Add(2)
		go h.SendOne(time.Second*0, addr, pid)
		go h.SendOne(slowHttpWait, addr, pid)
	}
}

// Wait for everything.
func (h *harness) Wait() {
	h.RequestWaitGroup.Wait()
}

// The main test case.
func TestComplex(t *testing.T) {
	debug("Started TestComplex")
	h := &harness{
		ImportPath: "github.com/daaku/go.grace/gracehttp/testserver",
		T:          t,
	}
	debug("Building")
	h.Build()
	debug("Initial Start")
	h.Start()
	debug("Send Request 1")
	h.SendRequest()
	debug("Sleeping 1")
	time.Sleep(processWait)
	debug("Restart 1")
	h.Restart()
	debug("Send Request 2")
	h.SendRequest()
	debug("Sleeping 2")
	time.Sleep(processWait)
	debug("Restart 2")
	h.Restart()
	debug("Send Request 3")
	h.SendRequest()
	debug("Sleeping 3")
	time.Sleep(processWait)
	debug("Stopping")
	h.Stop()
	debug("Waiting")
	h.Wait()
	debug("Removing Executable")
	h.RemoveExe()
}
