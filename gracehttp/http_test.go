package gracehttp_test

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/daaku/go.freeport"
	"github.com/daaku/go.tool"
)

// Debug logging.
var debugLog = flag.Bool("debug", false, "enable debug logging")

func debug(format string, a ...interface{}) {
	if *debugLog {
		println(fmt.Sprintf(format, a...))
	}
}

// The response from the test server.
type response struct {
	Sleep time.Duration
	Pid   int
	Error string
}

// State for the test run.
type harness struct {
	T                 *testing.T     // The test instance.
	ImportPath        string         // The import path for the server command.
	ExeName           string         // The temp binary from the build.
	httpAddr          string         // The address for the http server.
	httpsAddr         string         // The address for the https server.
	Process           []*os.Process  // The server commands, oldest to newest.
	ProcessMutex      sync.Mutex     // The mutex to guard Process manipulation.
	RequestWaitGroup  sync.WaitGroup // The wait group for the HTTP requests.
	newProcess        chan bool      // A bool is sent on start/restart.
	requestCount      int
	requestCountMutex sync.Mutex
}

// Find 3 free ports and setup addresses.
func (h *harness) SetupAddr() {
	port, err := freeport.Get()
	if err != nil {
		h.T.Fatalf("Failed to find a free port: %s", err)
	}
	h.httpAddr = fmt.Sprintf("127.0.0.1:%d", port)

	port, err = freeport.Get()
	if err != nil {
		h.T.Fatalf("Failed to find a free port: %s", err)
	}
	h.httpsAddr = fmt.Sprintf("127.0.0.1:%d", port)
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
	h.SetupAddr()
	cmd := exec.Command(h.ExeName, "-http", h.httpAddr, "-https", h.httpsAddr)
	stderr, err := cmd.StderrPipe()
	go func() {
		reader := bufio.NewReader(stderr)
		for {
			line, isPrefix, err := reader.ReadLine()
			if err != nil {
				log.Fatalf("Failed to read line from server process: %s", err)
			}
			if isPrefix {
				log.Fatalf("Deal with isPrefix for line: %s", line)
			}
			res := &response{}
			err = json.Unmarshal([]byte(line), res)
			if err != nil {
				log.Fatalf("Could not parse json from stderr %s: %s", line, err)
			}
			if res.Error != "" {
				println(fmt.Sprintf("Got error from process: %v", res))
			}
			process, err := os.FindProcess(res.Pid)
			if err != nil {
				log.Fatalf("Could not find process with pid: %d", res.Pid)
			}
			h.ProcessMutex.Lock()
			h.Process = append(h.Process, process)
			h.ProcessMutex.Unlock()
			h.newProcess <- true
		}
	}()
	err = cmd.Start()
	if err != nil {
		h.T.Fatalf("Failed to start command: %s", err)
	}
	<-h.newProcess
}

// Restart the most recent server.
func (h *harness) Restart() {
	err := h.MostRecentProcess().Signal(syscall.SIGUSR2)
	if err != nil {
		h.T.Fatalf("Failed to send SIGUSR2 and restart process: %s", err)
	}
	<-h.newProcess
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
	h.ProcessMutex.Lock()
	defer h.ProcessMutex.Unlock()
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

// Get the global request count and increment it.
func (h *harness) RequestCount() int {
	h.requestCountMutex.Lock()
	defer h.requestCountMutex.Unlock()
	c := h.requestCount
	h.requestCount++
	return c
}

// Helper for sending a single request.
func (h *harness) SendOne(dialgroup *sync.WaitGroup, url string, pid int) {
	defer h.RequestWaitGroup.Done()
	count := h.RequestCount()
	debug("Send %02d pid=%d url=%s", count, pid, url)
	client := &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
			Dial: func(network, addr string) (net.Conn, error) {
				defer dialgroup.Done()
				return net.Dial(network, addr)
			},
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	r, err := client.Get(url)
	if err != nil {
		h.T.Fatalf("Failed request %02d to %s pid=%d: %s", count, url, pid, err)
	}
	debug("Body %02d pid=%d url=%s", count, pid, url)
	defer r.Body.Close()
	res := &response{}
	err = json.NewDecoder(r.Body).Decode(res)
	if err != nil {
		h.T.Fatalf("Failed to ready decode json response body pid=%d: %s", pid, err)
	}
	if pid != res.Pid {
		h.T.Fatalf("Didn't get expected pid %d instead got %d", pid, res.Pid)
	}
	debug("Done %02d pid=%d url=%s", count, pid, url)
}

// Send test HTTP request.
func (h *harness) SendRequest() {
	pid := h.MostRecentProcess().Pid
	httpFastUrl := fmt.Sprintf("http://%s/sleep/?duration=0", h.httpAddr)
	httpSlowUrl := fmt.Sprintf("http://%s/sleep/?duration=2s", h.httpAddr)
	httpsFastUrl := fmt.Sprintf("https://%s/sleep/?duration=0", h.httpsAddr)
	httpsSlowUrl := fmt.Sprintf("https://%s/sleep/?duration=2s", h.httpsAddr)

	var dialgroup sync.WaitGroup
	h.RequestWaitGroup.Add(4)
	dialgroup.Add(4)
	go h.SendOne(&dialgroup, httpFastUrl, pid)
	go h.SendOne(&dialgroup, httpSlowUrl, pid)
	go h.SendOne(&dialgroup, httpsFastUrl, pid)
	go h.SendOne(&dialgroup, httpsSlowUrl, pid)
	debug("Added Requests pid=%d", pid)
	dialgroup.Wait()
	debug("Dialed Requests pid=%d", pid)
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
		newProcess: make(chan bool),
	}
	debug("Building")
	h.Build()
	debug("Initial Start")
	h.Start()
	debug("Send Request 1")
	h.SendRequest()
	debug("Restart 1")
	h.Restart()
	debug("Send Request 2")
	h.SendRequest()
	debug("Restart 2")
	h.Restart()
	debug("Send Request 3")
	h.SendRequest()
	debug("Waiting")
	h.Wait()
	debug("Stopping")
	h.Stop()
	debug("Removing Executable")
	h.RemoveExe()
}
