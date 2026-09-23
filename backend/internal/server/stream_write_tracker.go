package server

import (
	"io"
	"net/http"
	"time"
)

type streamWriteTracker struct {
	writer       io.Writer
	wrote        bool
	bytesWritten int64
	// firstWriteAt records when the first byte reached the client. It is stamped
	// only when a write actually produced bytes, even when ensureStarted
	// committed the response earlier, so it is a byte time, not a commit time.
	// Used only for observability and never gates control flow.
	firstWriteAt time.Time
	// committedAt records when ensureStarted committed the 200 response before
	// any byte was written. Handlers synthesize the first-byte time from it for a
	// confirmed empty-body success; it is never used when bytes were written or
	// the stream failed.
	committedAt time.Time
	// onFirstWrite runs once, just before the first byte is written. Response
	// headers must wait until that moment: failover can move to another candidate,
	// and writing early would expose the preferred route rather than the one that
	// actually served the request.
	onFirstWrite func()
}

func (w *streamWriteTracker) Write(data []byte) (int, error) {
	attemptedAt := time.Now()
	if !w.wrote {
		if w.onFirstWrite != nil {
			w.onFirstWrite()
		}
		if responseWriter, ok := w.writer.(http.ResponseWriter); ok {
			responseWriter.WriteHeader(http.StatusOK)
			if flusher, ok := responseWriter.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
	n, err := w.writer.Write(data)
	if n > 0 && w.firstWriteAt.IsZero() {
		// Only a positive write is a byte the client could perceive: a
		// disconnected client can yield (0, err), which must not count as a
		// first byte for TTFB or interruption.
		w.firstWriteAt = attemptedAt
	}
	w.wrote = true
	w.bytesWritten = saturatingAddNonNegative(w.bytesWritten, int64(n))
	return n, err
}

func (w *streamWriteTracker) Wrote() bool {
	return w != nil && w.wrote
}

func (w *streamWriteTracker) WroteData() bool {
	return w != nil && w.bytesWritten > 0
}

// ensureStarted runs the deferred hook even when the upstream produced no bytes.
// A 200 response with an empty body would otherwise reach the client with none of
// the headers onFirstWrite installs, including content-type. It commits the
// response but does not stamp firstWriteAt: a commit is not a byte, so the
// first-byte time stays zero until a real write happens.
func (w *streamWriteTracker) ensureStarted() {
	if w == nil || w.wrote {
		return
	}
	w.committedAt = time.Now()
	if w.onFirstWrite != nil {
		w.onFirstWrite()
	}
	if responseWriter, ok := w.writer.(http.ResponseWriter); ok {
		responseWriter.WriteHeader(http.StatusOK)
	}
	w.wrote = true
}

// firstByteTime returns when the first byte reached the client. An empty-body
// success has no byte to point at, so the commit time — the moment the client
// finally saw the 200 — stands in for it. A failed stream with zero bytes wrote
// nothing the client could perceive, so it reports zero.
func (w *streamWriteTracker) firstByteTime(success bool) time.Time {
	if w == nil {
		return time.Time{}
	}
	if !w.firstWriteAt.IsZero() {
		return w.firstWriteAt
	}
	if success {
		return w.committedAt
	}
	return time.Time{}
}

func (w *streamWriteTracker) Flush() {
	if w == nil {
		return
	}
	if flusher, ok := w.writer.(http.Flusher); ok {
		flusher.Flush()
	}
}
