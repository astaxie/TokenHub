package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// serverSentEvent is one decoded SSE frame.
type serverSentEvent struct {
	Event string
	Data  string
	// Raw holds every byte the frame consumed, terminator included, so a
	// pass-through path can forward the provider's exact framing instead of
	// re-rendering it from the decoded fields.
	Raw []byte
}

// maxSSEEventBytes bounds a single decoded event. bufio.Scanner's 64 KiB
// default is too small for reasoning and tool-argument frames, but an unbounded
// reader lets a faulty or hostile upstream grow one event until the process runs
// out of memory.
const maxSSEEventBytes = 8 << 20

// sseEventAssembler turns SSE lines into events. Both the pull-mode decoder and
// the push-mode writer below delegate framing to it, so every stream the gateway
// parses shares one set of field rules and one size limit.
type sseEventAssembler struct {
	event serverSentEvent
	data  []string
	raw   []byte
	// fields records that a semantic field (data, event, id, ...) is waiting for
	// its terminator. Comments are deliberately not semantic: on their own they
	// are a complete keepalive, but inside a frame they belong to it.
	fields bool
	size   int
}

// remaining reports how many more bytes the event being assembled may consume.
func (a *sseEventAssembler) remaining() int {
	return maxSSEEventBytes - a.size
}

// charge books bytes against the current event's budget. Callers charge before
// buffering so an event that never terminates is stopped while it is being read
// rather than after it has already been allocated.
func (a *sseEventAssembler) charge(n int) error {
	a.size += n
	if a.size > maxSSEEventBytes {
		// The frame is being abandoned, so its bytes must not reach a client:
		// forwarding them is exactly what the limit exists to prevent.
		a.raw = nil
		// size is deliberately left over the limit. An assembler that tripped it
		// has lost the frame boundary it was tracking, so it stays failed rather
		// than resuming part-way through an event it never finished reading.
		return errSSEEventTooLarge()
	}
	return nil
}

func errSSEEventTooLarge() error {
	return invalidProviderResponseError("provider sent a stream event that exceeds the size limit")
}

// consumeLine feeds one line, its terminator included. The boolean result
// reports that the line closed a frame.
func (a *sseEventAssembler) consumeLine(line string) (serverSentEvent, bool) {
	a.raw = append(a.raw, line...)
	trimmed := strings.TrimRight(line, "\r\n")
	switch {
	case trimmed == "":
		// Blank line terminates the frame. A separator that closes no fields
		// still completes: holding it back would charge a stream of blank-line
		// heartbeats against one event budget until it tripped the limit, and
		// would keep those bytes from a pass-through client. Callers see an
		// event carrying no fields, which every decoder already ignores.
		return a.complete(), true
	case strings.HasPrefix(trimmed, ":"):
		// A comment needs no blank line to be complete, so one that opens no
		// frame is delivered on its own. Holding it back would strand a
		// keepalive-only stream — the client would see nothing until the
		// upstream finally sent a real frame, and every ping would be charged
		// against a single event budget until it tripped the size limit.
		// Inside a frame the comment stays part of it, so its bytes survive.
		if !a.fields {
			return a.complete(), true
		}
	default:
		name, value := splitSSEField(trimmed)
		a.fields = true
		switch name {
		case "event":
			a.event.Event = value
		case "data":
			a.data = append(a.data, value)
		default:
			// id/retry and unknown fields are not meaningful here.
		}
	}
	return serverSentEvent{}, false
}

// flush returns whatever is pending when the stream ends without a blank line.
// Trailing bytes that carry no fields are still returned so a pass-through path
// can forward them.
func (a *sseEventAssembler) flush() (serverSentEvent, bool) {
	if len(a.raw) == 0 {
		return serverSentEvent{}, false
	}
	return a.complete(), true
}

func (a *sseEventAssembler) complete() serverSentEvent {
	event := a.event
	event.Data = strings.Join(a.data, "\n")
	event.Raw = a.raw
	a.event = serverSentEvent{}
	a.data = nil
	a.raw = nil
	a.fields = false
	a.size = 0
	return event
}

// sseDecoder reads Server-Sent Events without the 64 KiB ceiling bufio.Scanner
// imposes by default.
type sseDecoder struct {
	reader    *bufio.Reader
	assembler sseEventAssembler
}

func newSSEDecoder(r io.Reader) *sseDecoder {
	return &sseDecoder{reader: bufio.NewReader(r)}
}

// Next returns the next event, or io.EOF once the stream is exhausted.
func (d *sseDecoder) Next() (serverSentEvent, error) {
	for {
		line, err := d.readLine()
		// The line is consumed before the error is acted on: a read that
		// partially succeeded still returns bytes, and Pending has to see them.
		if line != "" {
			if event, done := d.assembler.consumeLine(line); done {
				return event, nil
			}
		}
		switch {
		case err == nil:
		case err == io.EOF:
			if event, ok := d.assembler.flush(); ok {
				return event, nil
			}
			return serverSentEvent{}, io.EOF
		default:
			return serverSentEvent{}, err
		}
	}
}

// PendingEvent returns the parsed fields and raw framing accumulated before a
// mid-frame read failure. A pass-through flushes it because the upstream already
// put those bytes on the wire; dropping them would truncate the client's copy
// further than the failure itself did. An event abandoned for exceeding the
// size limit reports no raw bytes. Reading it does not consume or reset the
// assembler, so it is only meaningful after the caller has stopped.
func (d *sseDecoder) PendingEvent() serverSentEvent {
	event := d.assembler.event
	event.Data = strings.Join(d.assembler.data, "\n")
	event.Raw = d.assembler.raw
	return event
}

// readLine reads one line while charging its bytes against the event budget.
// bufio.Reader.ReadString would allocate the whole line before returning, so a
// line that never terminates could exhaust memory before any limit was checked.
// ReadSlice hands back bounded chunks, letting the budget stop the read early.
func (d *sseDecoder) readLine() (string, error) {
	var builder strings.Builder
	for {
		chunk, err := d.reader.ReadSlice('\n')
		if chargeErr := d.assembler.charge(len(chunk)); chargeErr != nil {
			return "", chargeErr
		}
		builder.Write(chunk)
		if err == bufio.ErrBufferFull {
			continue
		}
		return builder.String(), err
	}
}

// sseStreamWriter decodes events from a stream that is pushed at it as an
// io.Writer. Upstream copies hand the gateway bytes rather than a reader, and
// accumulating those bytes without a limit is what lets one event grow until the
// process runs out of memory.
type sseStreamWriter struct {
	assembler sseEventAssembler
	pending   []byte
	handle    func(serverSentEvent) error
	closed    bool
	// failure latches the error that abandoned the frame in progress. Without
	// it Close would hand the handler the accepted prefix of a frame that was
	// just rejected, delivering part of an event the writer refused.
	failure error
}

func newSSEStreamWriter(handle func(serverSentEvent) error) *sseStreamWriter {
	return &sseStreamWriter{handle: handle}
}

// Write splits the pushed bytes into lines as it goes. Only the trailing
// unterminated line is buffered, so the budget bounds what the writer holds
// without rejecting a write that merely happens to carry many whole events.
func (w *sseStreamWriter) Write(data []byte) (int, error) {
	if w.failure != nil {
		return 0, w.failure
	}
	total := len(data)
	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		if index < 0 {
			if len(w.pending)+len(data) > w.assembler.remaining() {
				return total - len(data), w.fail(errSSEEventTooLarge())
			}
			w.pending = append(w.pending, data...)
			break
		}
		// Checked before the line is materialised: allocating an oversized line
		// and only then refusing it is the allocation the budget exists to stop.
		if len(w.pending)+index+1 > w.assembler.remaining() {
			return total - len(data), w.fail(errSSEEventTooLarge())
		}
		line := string(data[:index+1])
		if len(w.pending) > 0 {
			line = string(w.pending) + line
			w.pending = w.pending[:0]
		}
		data = data[index+1:]
		// The check above already guarantees the line fits; charge still has to
		// run to advance the budget, and its error stays as a backstop.
		if err := w.assembler.charge(len(line)); err != nil {
			return total - len(data), w.fail(err)
		}
		if event, done := w.assembler.consumeLine(line); done {
			if err := w.handle(event); err != nil {
				return total - len(data), w.fail(err)
			}
		}
	}
	return total, nil
}

// fail latches an error so nothing further is decoded or delivered.
func (w *sseStreamWriter) fail(err error) error {
	w.failure = err
	return err
}

// Close delivers the frame left pending when the upstream stops without a
// terminating blank line. Dropping it would silently truncate the response.
func (w *sseStreamWriter) Close() error {
	if w.closed || w.failure != nil {
		// Whatever is buffered belongs to the frame that failed. Delivering it
		// would emit part of an event the writer already refused.
		return nil
	}
	w.closed = true
	if len(w.pending) > 0 {
		line := string(w.pending)
		w.pending = nil
		if err := w.assembler.charge(len(line)); err != nil {
			return w.fail(err)
		}
		if event, done := w.assembler.consumeLine(line); done {
			return w.handle(event)
		}
	}
	event, ok := w.assembler.flush()
	if !ok {
		return nil
	}
	return w.handle(event)
}

func splitSSEField(line string) (string, string) {
	colon := strings.Index(line, ":")
	if colon < 0 {
		return line, ""
	}
	name := line[:colon]
	value := line[colon+1:]
	// A single leading space after the colon is part of the framing.
	value = strings.TrimPrefix(value, " ")
	return name, value
}

// rewriteSSEEventData returns the frame's raw bytes with its data lines replaced
// by a single data line carrying payload. Comments, event names and the blank
// terminator are copied byte for byte: a gateway that rewrites the payload still
// owes the client the framing the provider chose.
//
// A frame whose payload arrived across several data lines is re-framed onto one,
// because the re-encoded JSON has no correspondence to the split the provider
// chose and any other division risks cutting a string literal in half. Receivers
// join data lines with "\n" before parsing, so the payload a client reconstructs
// is unchanged; only the number of lines carrying it differs.
func rewriteSSEEventData(raw []byte, payload string) []byte {
	output := make([]byte, 0, len(raw)+len(payload))
	written := false
	for len(raw) > 0 {
		line := raw
		if index := bytes.IndexByte(raw, '\n'); index >= 0 {
			line, raw = raw[:index+1], raw[index+1:]
		} else {
			raw = nil
		}
		if !isSSEDataLine(line) {
			output = append(output, line...)
			continue
		}
		if written {
			// Multi-line data was joined into one payload, so the follow-up
			// lines have already been folded into the replacement.
			continue
		}
		written = true
		output = append(output, "data: "...)
		output = append(output, payload...)
		// The provider's own terminator is reused. Normalising CRLF to LF here
		// would leave the rewritten line framed differently from every other
		// line in the same frame.
		output = append(output, sseLineTerminator(line)...)
	}
	return output
}

// sseLineTerminator returns the line ending the given line carried, which is
// empty for the unterminated final line of a truncated stream.
func sseLineTerminator(line []byte) []byte {
	switch {
	case bytes.HasSuffix(line, []byte("\r\n")):
		return []byte("\r\n")
	case bytes.HasSuffix(line, []byte("\n")):
		return []byte("\n")
	default:
		return nil
	}
}

func isSSEDataLine(line []byte) bool {
	name, _ := splitSSEField(strings.TrimRight(string(line), "\r\n"))
	return name == "data"
}

// decodeSSEData unmarshals an event payload, rejecting malformed frames rather
// than skipping them: a dropped frame silently truncates the response.
func decodeSSEData(event serverSentEvent) (map[string]any, error) {
	payload := strings.TrimSpace(event.Data)
	if payload == "" {
		return nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil, invalidProviderResponseError(fmt.Sprintf("provider sent a malformed stream event: %v", err))
	}
	return decoded, nil
}

// decodeSSEDataNumbers is decodeSSEData with JSON numbers preserved verbatim, so
// a re-encoded frame keeps the precision the provider sent.
func decodeSSEDataNumbers(event serverSentEvent) (map[string]any, bool, error) {
	payload := strings.TrimSpace(event.Data)
	if payload == "" || payload == "[DONE]" {
		return nil, false, nil
	}
	var decoded map[string]any
	reader := json.NewDecoder(strings.NewReader(payload))
	reader.UseNumber()
	if err := reader.Decode(&decoded); err != nil {
		return nil, false, err
	}
	// Decode stops at the end of the first value. Anything after it would be lost
	// when the frame is re-encoded, so a payload that is not exhausted here is
	// rejected rather than silently truncated.
	var trailing json.RawMessage
	if err := reader.Decode(&trailing); err != io.EOF {
		return nil, false, fmt.Errorf("stream event carries trailing data after its JSON value")
	}
	return decoded, true, nil
}
