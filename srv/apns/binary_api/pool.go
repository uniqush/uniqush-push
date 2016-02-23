package binary_api

// Worker pool implementation for apns.
import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const CLOSE_TIMEOUT = time.Hour

// PermanentError is returned by a worker because of an error that can't be fixed at the moment (e.g. a connection couldn't be established)
type PermanentError struct {
	Err error
}

func (self *PermanentError) Error() string {
	return fmt.Sprintf("We are unable to get a connection: %s", self.Err.Error())
}

// TemporaryError is returned by a worker for problems that can be fixed by retrying.
type TemporaryError struct {
	Err      error
	Endpoint net.Addr
}

func (self *TemporaryError) Error() string {
	return fmt.Sprintf("Error connecting to %v: %v", self.Endpoint, self.Err.Error())
}

// Pool is a fixed-size pool of workers responsible for sending payloads to APNS.
type Pool struct {
	manager ConnManager

	wg          sync.WaitGroup
	requests    chan workerRequest
	isClosed    bool
	maxWorkers  int
	maxWaitTime int
}

// workerRequest contains all information needed for a worker to send the payload to APNS and reply to the requester.
type workerRequest struct {
	Payload  []byte
	Response chan<- error
}

// NewPool creates a thread with numWorkers workers, which will wait until they are first needed to open a connection.
func NewPool(manager ConnManager, numWorkers int, maxWaitTime int) *Pool {
	ret := Pool{
		manager:     manager,
		isClosed:    false,
		requests:    make(chan workerRequest),
		maxWorkers:  numWorkers,
		maxWaitTime: maxWaitTime,
	}
	// Create exactly numWorkers threads.
	ret.wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go poolWorker(&ret.wg, ret.manager, ret.requests, maxWaitTime)
	}
	return &ret
}

// Close waits for all workers to close their connections and finish executing.
func (self *Pool) Close() {
	// Close the request, causing each of the numWorkers to call numWorkers to close
	close(self.requests)
	// Wait for all of the worker goroutines to exit cleanly.
	self.wg.Wait()
}

// Push will send the push payload to APNS, and respond with nil, TemporaryError, or PermanentError.
func (self *Pool) Push(payload []byte) error {
	responseChan := make(chan error)
	request := workerRequest{
		Payload:  payload,
		Response: responseChan,
	}

	// Send a request to a worker in a blocking manner
	self.requests <- request
	return <-responseChan
}

// poolWorker sends data to APNS. A different connection receives the APNS responses.
func poolWorker(wg *sync.WaitGroup, manager ConnManager, requests <-chan workerRequest, maxWaitTime int) {
	defer wg.Done()
	var conn net.Conn = nil
	var err error
	var closed <-chan bool

	lastRequestTime := time.Now()
	for request := range requests {
		curTime := time.Now()
		if conn != nil {
			// Ensure we'll automatically reconnect on a request if we know the connection was closed. This is non-blocking.
			select {
			case <-closed:
				conn = nil
			default:
			}
		}
		if conn != nil && curTime.Sub(lastRequestTime) > CLOSE_TIMEOUT {
			// Close and re-open the connection if the connection has been inactive for over an hour.
			conn.Close()
			conn = nil
			closed = nil
		}
		lastRequestTime = curTime

		if conn == nil {
			// Lazily attempt to open a connection. If that fails, tell the requester that the connection failed.
			conn, closed, err = manager.NewConn()
			if err != nil {
				request.Response <- &PermanentError{Err: err}
				conn = nil
				continue
			}
		}
		deadline := time.Now().Add(time.Duration(maxWaitTime) * time.Second)
		conn.SetWriteDeadline(deadline)
		err := writen(conn, request.Payload)
		if err != nil {
			request.Response <- &TemporaryError{Err: err, Endpoint: conn.RemoteAddr()}
			conn.Close()
			conn = nil
			closed = nil
			continue
		}
		request.Response <- nil
	}
	if conn != nil {
		conn.Close()
		conn = nil
		closed = nil
	}
}

// writen is a safe wrapper around Write, specialized for `net.Conn`s.
// writen will continue calling write until it finishes or an error is encountered.
// It will abort after 10 "temporary errors" - it's gotten into a busy loop and ate 100% of a CPU once.
func writen(w io.Writer, buf []byte) error {
	remainingTemporaryErrors := 10
	n := len(buf)
	for n >= 0 {
		l, err := w.Write(buf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Temporary() && !nerr.Timeout() {
				if remainingTemporaryErrors > 0 {
					remainingTemporaryErrors--
					continue
				}
			}
			return err
		}
		if l >= n {
			return nil
		}
		n -= l
		buf = buf[l:]
	}
	return nil
}
