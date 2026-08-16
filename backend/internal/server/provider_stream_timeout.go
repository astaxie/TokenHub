package server

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Streaming and non-streaming upstream calls need different timeout policies.
// http.Client.Timeout bounds the whole exchange including reading the response
// body, so a single total deadline truncates an SSE stream that is still
// delivering data — a long reasoning or coding response would stop mid-answer
// with no [DONE]. A streaming call therefore runs with no total deadline and is
// bounded instead by how long it has been silent: a timer that restarts on every
// delivered byte tells a slow model apart from a dead connection, which a total
// deadline cannot.
const (
	// defaultUpstreamNonStreamTimeoutSeconds bounds one non-streaming upstream
	// exchange end to end.
	defaultUpstreamNonStreamTimeoutSeconds = 120
	// defaultUpstreamStreamIdleTimeoutSeconds bounds how long a streaming call
	// may wait for response headers, and how long its body may then stay silent.
	defaultUpstreamStreamIdleTimeoutSeconds = 300
	// maxUpstreamTimeoutSeconds is the largest value accepted. Anything beyond it
	// is treated as a misconfiguration and replaced by the default rather than
	// honoured, since it would overflow when multiplied into a time.Duration.
	maxUpstreamTimeoutSeconds = 86400
)

var errProviderStreamIdle = NewHTTPError(
	http.StatusGatewayTimeout,
	"provider_stream_idle_timeout",
	"Upstream stream was idle for too long",
)

// newUpstreamClients builds the pair every HTTP provider adapter is given, plus
// the idle budget the streaming one is paired with. Two clients rather than one
// because http.Client.Timeout covers reading the response body: a total deadline
// that suits a normal request truncates a stream that is still delivering.
//
// Both clients validate the actual request before sending credentials, dial
// through the SSRF guard, and follow the strict redirect policy. Save-time
// validation alone cannot protect old records or later DNS rebinding, and a
// redirect must never bounce inference traffic into the internal network.
func newUpstreamClients(config Config, syntheticDNS ...*providerSyntheticDNSPolicy) (*http.Client, *http.Client, time.Duration) {
	idleTimeout := upstreamTimeout(config.UpstreamStreamIdleTimeoutSeconds, defaultUpstreamStreamIdleTimeoutSeconds)
	allowedPrivate := allowedProviderUpstreamCIDRs()
	var syntheticDNSPolicy *providerSyntheticDNSPolicy
	if len(syntheticDNS) > 0 {
		syntheticDNSPolicy = syntheticDNS[0]
	}
	client := &http.Client{
		Timeout:       upstreamTimeout(config.UpstreamNonStreamTimeoutSeconds, defaultUpstreamNonStreamTimeoutSeconds),
		Transport:     guardProviderUpstreamRequests(rotatingProviderUpstreamTransport(allowedPrivate, syntheticDNSPolicy, nil), allowedPrivate),
		CheckRedirect: strictProviderUpstreamRedirect,
	}
	return client, newUpstreamStreamClient(idleTimeout, syntheticDNS...), idleTimeout
}

// newUpstreamStreamClient returns the client used for streaming upstream calls:
// no total deadline, and a header timeout matching the idle budget so a stream
// that never starts fails on the same terms as one that stops. It dials through
// the same SSRF-guarded transport as the non-streaming client.
func newUpstreamStreamClient(idleTimeout time.Duration, syntheticDNS ...*providerSyntheticDNSPolicy) *http.Client {
	// Each transport generation clones the default transport (never mutates the
	// process-global one), installs the guarded DialContext with proxying disabled,
	// and adds the streaming response-header timeout.
	allowedPrivate := allowedProviderUpstreamCIDRs()
	var syntheticDNSPolicy *providerSyntheticDNSPolicy
	if len(syntheticDNS) > 0 {
		syntheticDNSPolicy = syntheticDNS[0]
	}
	transport := rotatingProviderUpstreamTransport(allowedPrivate, syntheticDNSPolicy, func(transport *http.Transport) {
		transport.ResponseHeaderTimeout = idleTimeout
	})
	return &http.Client{Transport: guardProviderUpstreamRequests(transport, allowedPrivate), CheckRedirect: strictProviderUpstreamRedirect}
}

// upstreamTimeout converts a configured second count into a duration, falling
// back to fallbackSeconds when the value is absent or out of range.
func upstreamTimeout(seconds int, fallbackSeconds int) time.Duration {
	if seconds <= 0 || seconds > maxUpstreamTimeoutSeconds {
		seconds = fallbackSeconds
	}
	return time.Duration(seconds) * time.Second
}

// idleTimeoutReadCloser fails a response body that has gone silent. Reading
// resumes the budget rather than consuming it, so a stream stays open for as
// long as the upstream keeps producing.
type idleTimeoutReadCloser struct {
	source     io.ReadCloser
	timeout    time.Duration
	timeoutErr *HTTPError
	timerMu    sync.Mutex
	timer      *time.Timer
	closed     bool
	timedOut   atomic.Bool
}

// newIdleTimeoutReadCloser starts the idle budget immediately rather than on the
// first Read: an upstream that returns headers and then says nothing has to fail
// on the same terms as one that goes silent mid-stream.
func newIdleTimeoutReadCloser(source io.ReadCloser, timeout time.Duration, timeoutErr *HTTPError) io.ReadCloser {
	if source == nil || timeout <= 0 {
		return source
	}
	if timeoutErr == nil {
		timeoutErr = errProviderStreamIdle
	}
	reader := &idleTimeoutReadCloser{source: source, timeout: timeout, timeoutErr: timeoutErr}
	reader.resetTimer()
	return reader
}

func (r *idleTimeoutReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.source.Read(buffer)
	// Not after the timer already fired: the source is closed, so extending the
	// budget would only arm a timer against a body nobody can read.
	if count > 0 && !r.timedOut.Load() {
		r.resetTimer()
	}
	if err != nil && r.timedOut.Load() {
		// The bytes already read are still returned: the timer closed the source
		// underneath a Read that had partially succeeded, and discarding them
		// would corrupt the stream rather than merely end it.
		return count, r.timeoutErr
	}
	return count, err
}

func (r *idleTimeoutReadCloser) Close() error {
	r.timerMu.Lock()
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	r.timerMu.Unlock()
	return r.source.Close()
}

func (r *idleTimeoutReadCloser) resetTimer() {
	r.timerMu.Lock()
	defer r.timerMu.Unlock()
	// A late Read after Close must not arm a new timer against a body nobody is
	// reading any more.
	if r.closed {
		return
	}
	if r.timer == nil {
		r.timer = time.AfterFunc(r.timeout, func() {
			r.timedOut.Store(true)
			_ = r.source.Close()
		})
		return
	}
	r.timer.Reset(r.timeout)
}

// sendUpstream issues one upstream exchange. A streaming call uses the client
// with no total deadline and gets a body that fails once it has gone silent; a
// non-streaming call keeps the whole-exchange bound. An error response is
// wrapped too: it is short, but with the total deadline gone it is the only
// thing left bounding a proxy that answers 502 and then stalls without sending
// the body it announced.
func sendUpstream(client *http.Client, streamClient *http.Client, idleTimeout time.Duration, req *http.Request, stream bool) (*http.Response, error) {
	chosen := client
	if stream && streamClient != nil {
		chosen = streamClient
	}
	if chosen == nil {
		chosen = http.DefaultClient
	}
	resp, err := chosen.Do(req)
	if err != nil || !stream {
		return resp, err
	}
	resp.Body = newIdleTimeoutReadCloser(resp.Body, idleTimeout, errProviderStreamIdle)
	return resp, nil
}
