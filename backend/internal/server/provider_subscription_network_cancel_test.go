package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

type subscriptionNetworkOutput struct {
	io.Reader
	drained chan struct{}
	finish  chan struct{}
}

func (r *subscriptionNetworkOutput) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	if err == io.EOF {
		close(r.drained)
		<-r.finish
	}
	return n, err
}

func TestSubscriptionWorkerCancelsQueuedNetworkRequest(t *testing.T) {
	// Consume both frames on one scheduler thread before allowing the network
	// goroutine to run, as happens when a fetch is aborted immediately.
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make(chan context.Context, 1)
	completed := make(chan struct{}, 1)
	p := &subscriptionWorkerProcess{ctx: ctx, cancel: cancel, cmd: &exec.Cmd{}, done: make(chan struct{}),
		pending: map[string]chan subscriptionWorkerEvent{}, networks: map[string]context.CancelFunc{}, directory: t.TempDir()}
	p.input = subscriptionTestInput{write: func(data []byte) (int, error) {
		var message map[string]any
		if err := json.Unmarshal(data, &message); err != nil {
			return 0, err
		}
		if message["operation"] == "network.error" {
			completed <- struct{}{}
		}
		return len(data), nil
	}}
	p.client = &http.Client{Transport: subscriptionWorkerRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests <- request.Context()
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	frames := "{\"event\":\"network.request\",\"request_id\":\"aborted\",\"method\":\"GET\",\"url\":\"https://api.cursor.com/test\"}\n" +
		"{\"event\":\"network.cancel\",\"request_id\":\"aborted\"}\n"
	output := &subscriptionNetworkOutput{Reader: strings.NewReader(frames), drained: make(chan struct{}), finish: make(chan struct{})}
	t.Cleanup(func() { close(output.finish); cancel(); <-p.done })
	go p.read(output)
	<-output.drained
	select {
	case request := <-requests:
		if request.Err() == nil {
			t.Fatal("The worker lost cancellation before the network goroutine registered")
		}
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("The canceled network request did not finish")
	}
}
