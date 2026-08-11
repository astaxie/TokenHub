package server

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// chunkedReader replays a stream in fixed-size pieces so a test can prove that
// event boundaries are reassembled rather than assumed to arrive whole.
type chunkedReader struct {
	data []byte
	size int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.size
	if n > len(r.data) {
		n = len(r.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// framingVariantsStream exercises every framing feature the decoder has to
// tolerate: a comment heartbeat, CRLF endings, a named event, a multi-line data
// payload, an event carrying no data at all and the [DONE] sentinel.
const framingVariantsStream = ": heartbeat\r\n" +
	"\r\n" +
	"event: chunk\r\n" +
	"data: {\"choices\":[{\"delta\":\r\n" +
	"data: {\"content\":\"hi\"}}]}\r\n" +
	"\r\n" +
	"event: ping\n" +
	"\n" +
	"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n" +
	"\n" +
	"data: [DONE]\n" +
	"\n"

func TestCopyOpenAIStreamPreservesFramingVariantsByteForByte(t *testing.T) {
	var output strings.Builder

	usage, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(framingVariantsStream))
	if err != nil {
		t.Fatal(err)
	}
	// The OpenAI-compatible path is a pass-through: whatever framing the
	// provider chose has to reach the client unchanged.
	if output.String() != framingVariantsStream {
		t.Fatalf("stream changed during copy:\n%q", output.String())
	}
	want := Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("stream usage = %+v, want %+v", usage, want)
	}
}

func TestCopyOpenAIStreamReassemblesEventsSplitAcrossReads(t *testing.T) {
	// A provider may flush an event in arbitrary pieces, so usage extraction
	// must not depend on a data line arriving in a single read.
	for _, size := range []int{1, 3, 17, 64} {
		var output strings.Builder
		reader := &chunkedReader{data: []byte(framingVariantsStream), size: size}

		usage, err := copyOpenAIStreamAndUsage(&output, reader)
		if err != nil {
			t.Fatalf("chunk size %d: %v", size, err)
		}
		if output.String() != framingVariantsStream {
			t.Fatalf("chunk size %d changed the stream:\n%q", size, output.String())
		}
		want := Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}
		if !reflect.DeepEqual(usage, want) {
			t.Fatalf("chunk size %d usage = %+v, want %+v", size, usage, want)
		}
	}
}

func TestCopyOpenAIStreamKeepsUnterminatedTailByteForByte(t *testing.T) {
	// A dropped connection leaves a partial frame. Truncating it further would
	// hide the truncation from the client.
	stream := "data: {\"choices\":[]}\n\ndata: {\"cho"
	var output strings.Builder

	if _, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(stream)); err != nil {
		t.Fatal(err)
	}
	if output.String() != stream {
		t.Fatalf("unterminated tail changed:\n%q", output.String())
	}
}

func TestCopyOpenAIStreamIgnoresMalformedEventForUsage(t *testing.T) {
	// A frame the gateway cannot parse still belongs to the client; only usage
	// accounting skips it.
	stream := "data: {not json}\n\ndata: {\"usage\":{\"total_tokens\":5,\"prompt_tokens\":4,\"completion_tokens\":1}}\n\n"
	var output strings.Builder

	usage, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != stream {
		t.Fatalf("stream changed during copy:\n%q", output.String())
	}
	if usage.TotalTokens != 5 {
		t.Fatalf("usage = %+v, want total 5", usage)
	}
}

func TestCopyNativeAnthropicStreamPreservesFramingAndRewritesModel(t *testing.T) {
	stream := ": heartbeat\r\n" +
		"\r\n" +
		"event: message_start\r\n" +
		"data: {\"message\":{\"model\":\"upstream-model\",\"usage\":{\"input_tokens\":11,\"output_tokens\":0}}}\r\n" +
		"\r\n" +
		"event: ping\n" +
		"\n" +
		"event: message_delta\n" +
		"data: {\"usage\":{\"output_tokens\":4}}\n" +
		"\n" +
		"data: [DONE]\n" +
		"\n"
	var output strings.Builder

	usage, err := copyNativeAnthropicStream(&output, strings.NewReader(stream), "gateway-model")
	if err != nil {
		t.Fatal(err)
	}
	got := output.String()
	// Only data frames are re-encoded, and only to swap in the gateway-facing
	// model name. Comments, event names and the [DONE] sentinel pass through.
	for _, want := range []string{
		": heartbeat\r\n\r\n",
		"event: message_start\r\n",
		"\"model\":\"gateway-model\"",
		"event: ping\n\n",
		"data: [DONE]\n\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output is missing %q:\n%q", want, got)
		}
	}
	if strings.Contains(got, "upstream-model") {
		t.Fatalf("upstream model name leaked to the client:\n%q", got)
	}
	want := Usage{PromptTokens: 11, CompletionTokens: 4, TotalTokens: 15}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
}

// openAIChunkStream is an OpenAI chat completion stream carrying text, a tool
// call and a usage frame, in the framing an upstream actually emits.
const openAIChunkStream = ": keep-alive\n" +
	"\n" +
	"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n" +
	"\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n" +
	"\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\"," +
	"\"function\":{\"name\":\"ping\",\"arguments\":\"{\\\"a\\\":1}\"}}]}}]}\n" +
	"\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]," +
	"\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":5,\"total_tokens\":14}}\n" +
	"\n" +
	"data: [DONE]\n" +
	"\n"

// runAnthropicConverter feeds a stream to the converter in fixed-size writes and
// returns the Anthropic events the client would receive.
func runAnthropicConverter(t *testing.T, stream string, chunk int) string {
	t.Helper()
	writer := &recordingWriter{}
	converter := newOpenAIAnthropicStreamConverter(writer, "gateway-model", 9, Provider{})
	for offset := 0; offset < len(stream); offset += chunk {
		end := offset + chunk
		if end > len(stream) {
			end = len(stream)
		}
		if _, err := converter.Write([]byte(stream[offset:end])); err != nil {
			t.Fatalf("chunk size %d: %v", chunk, err)
		}
	}
	if err := converter.Finalize(Usage{PromptTokens: 9, CompletionTokens: 5, TotalTokens: 14}); err != nil {
		t.Fatalf("chunk size %d finalize: %v", chunk, err)
	}
	return writer.builder.String()
}

func TestAnthropicConverterOutputIsIndependentOfChunkBoundaries(t *testing.T) {
	// The converter is written to as an io.Writer, so a data line can be split
	// across any number of calls. Reassembly must not depend on where the
	// upstream happened to flush.
	baseline := runAnthropicConverter(t, openAIChunkStream, len(openAIChunkStream))
	for _, want := range []string{
		"event: message_start\n",
		"\"text\":\"he\",\"type\":\"text_delta\"",
		"\"text\":\"llo\",\"type\":\"text_delta\"",
		"\"name\":\"ping\"",
		"\"stop_reason\":\"tool_use\"",
		"event: message_stop\n",
	} {
		if !strings.Contains(baseline, want) {
			t.Fatalf("converted stream is missing %q:\n%s", want, baseline)
		}
	}
	for _, chunk := range []int{1, 2, 7, 33} {
		if got := runAnthropicConverter(t, openAIChunkStream, chunk); got != baseline {
			t.Fatalf("chunk size %d changed the converted stream:\n%s\nwant:\n%s", chunk, got, baseline)
		}
	}
}

func TestAnthropicConverterConsumesFrameWithoutTrailingBlankLine(t *testing.T) {
	// An upstream that closes right after its last data line still owes the
	// client that frame's content.
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"tail\"}}]}\n"
	writer := &recordingWriter{}
	converter := newOpenAIAnthropicStreamConverter(writer, "gateway-model", 1, Provider{})
	if _, err := converter.Write([]byte(stream)); err != nil {
		t.Fatal(err)
	}
	if err := converter.closeStream(); err != nil {
		t.Fatal(err)
	}
	if converter.outputText.String() != "tail" {
		t.Fatalf("output text = %q, want %q", converter.outputText.String(), "tail")
	}
}

func TestCopyNativeAnthropicStreamRejectsInvalidJSON(t *testing.T) {
	var output strings.Builder

	_, err := copyNativeAnthropicStream(&output, strings.NewReader("data: {not json}\n\n"), "gateway-model")
	if err == nil {
		t.Fatal("invalid provider JSON must fail the stream")
	}
	status, _ := statusAndCode(err)
	if status != 502 {
		t.Fatalf("status = %d, want 502", status)
	}
}

// oversizedDataLines builds one SSE frame out of many small data lines whose
// combined size passes the limit. Charging per line rather than per read is what
// makes this case bounded.
func oversizedDataLines() string {
	line := "data: " + strings.Repeat("x", 4096) + "\n"
	var builder strings.Builder
	for builder.Len() <= maxSSEEventBytes {
		builder.WriteString(line)
	}
	return builder.String()
}

func TestCopyOpenAIStreamRejectsUnterminatedOversizedEvent(t *testing.T) {
	// Before the limit, a data line that never ends grew until the process ran
	// out of memory. It now has to fail the stream instead.
	var output strings.Builder
	body := io.LimitReader(neverEndingReader{}, int64(maxSSEEventBytes)*2)

	if _, err := copyOpenAIStreamAndUsage(&output, body); err == nil {
		t.Fatal("an unterminated event beyond the size limit must be rejected")
	}
}

func TestCopyOpenAIStreamRejectsOversizedMultiLineEvent(t *testing.T) {
	var output strings.Builder

	if _, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(oversizedDataLines())); err == nil {
		t.Fatal("a frame whose data lines exceed the size limit must be rejected")
	}
}

func TestCopyNativeAnthropicStreamRejectsOversizedEvent(t *testing.T) {
	var output strings.Builder
	body := io.LimitReader(neverEndingReader{}, int64(maxSSEEventBytes)*2)

	if _, err := copyNativeAnthropicStream(&output, body, "gateway-model"); err == nil {
		t.Fatal("an unterminated event beyond the size limit must be rejected")
	}
}

func TestAnthropicConverterRejectsOversizedEvent(t *testing.T) {
	// /v1/messages is the public inbound path: a faulty or hostile upstream used
	// to be able to grow the converter's buffer without any ceiling.
	converter := newOpenAIAnthropicStreamConverter(&recordingWriter{}, "gateway-model", 1, Provider{})
	chunk := []byte(strings.Repeat("x", 1<<20))

	var err error
	for written := 0; written <= 2*maxSSEEventBytes && err == nil; written += len(chunk) {
		_, err = converter.Write(chunk)
	}
	if err == nil {
		t.Fatal("an unterminated event beyond the size limit must be rejected")
	}
}

func TestAnthropicConverterRejectsOversizedMultiLineEvent(t *testing.T) {
	converter := newOpenAIAnthropicStreamConverter(&recordingWriter{}, "gateway-model", 1, Provider{})

	if _, err := converter.Write([]byte(oversizedDataLines())); err == nil {
		t.Fatal("a frame whose data lines exceed the size limit must be rejected")
	}
}

func TestSSEStreamWriterMatchesDecoderFraming(t *testing.T) {
	// The push and pull decoders share one assembler; this pins that they stay
	// interchangeable for the framing real providers emit.
	var pushed []serverSentEvent
	writer := newSSEStreamWriter(func(event serverSentEvent) error {
		pushed = append(pushed, event)
		return nil
	})
	for _, b := range []byte(framingVariantsStream) {
		if _, err := writer.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	decoder := newSSEDecoder(strings.NewReader(framingVariantsStream))
	var pulled []serverSentEvent
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		pulled = append(pulled, event)
	}

	if !reflect.DeepEqual(pushed, pulled) {
		t.Fatalf("push decoding = %+v, want %+v", pushed, pulled)
	}
}

func TestSSEDecoderJoinsMultiLineDataForUsage(t *testing.T) {
	// A usage frame split over several data lines used to be invisible to
	// accounting, because each line was parsed as if it were a whole payload.
	stream := "data: {\"usage\":\n" +
		"data: {\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n" +
		"\n"
	var output strings.Builder

	usage, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != stream {
		t.Fatalf("stream changed during copy:\n%q", output.String())
	}
	want := Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
}

// failingReader replays a stream and then fails, the way the idle-timeout reader
// does when a provider goes silent part-way through a frame.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestCopyOpenAIStreamForwardsPendingBytesWhenTheStreamFails(t *testing.T) {
	// idleTimeoutReadCloser returns bytes alongside its error on purpose, and a
	// complete data line still waiting for its blank terminator is one the
	// client already had under the previous line-at-a-time copy.
	stream := "data: {\"choices\":[]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n"
	failure := NewHTTPError(504, "provider_stream_idle", "provider stalled")
	var output strings.Builder

	_, err := copyOpenAIStreamAndUsage(&output, &failingReader{data: []byte(stream), err: failure})
	if err != failure {
		t.Fatalf("err = %v, want the upstream failure", err)
	}
	if output.String() != stream {
		t.Fatalf("bytes were dropped on failure:\n%q", output.String())
	}
}

func TestCopyNativeAnthropicStreamForwardsPendingBytesWhenTheStreamFails(t *testing.T) {
	stream := "event: message_start\ndata: {\"message\":{}}\n\nevent: content_block_delta\n"
	failure := NewHTTPError(504, "provider_stream_idle", "provider stalled")
	var output strings.Builder

	_, err := copyNativeAnthropicStream(&output, &failingReader{data: []byte(stream), err: failure}, "gateway-model")
	if err != failure {
		t.Fatalf("err = %v, want the upstream failure", err)
	}
	if !strings.HasSuffix(output.String(), "event: content_block_delta\n") {
		t.Fatalf("the trailing frame was dropped on failure:\n%q", output.String())
	}
}

func TestCopyOpenAIStreamWithholdsBytesOfAnOversizedEvent(t *testing.T) {
	// Forwarding what was read before the limit tripped would hand the client
	// megabytes of the frame the limit exists to refuse.
	var output strings.Builder
	body := io.LimitReader(neverEndingReader{}, int64(maxSSEEventBytes)*2)

	if _, err := copyOpenAIStreamAndUsage(&output, body); err == nil {
		t.Fatal("an oversized event must be rejected")
	}
	if output.Len() != 0 {
		t.Fatalf("oversized event leaked %d bytes to the client", output.Len())
	}
}

func TestSSEDecoderDoesNotAccumulateBlankLineHeartbeats(t *testing.T) {
	// A stream of bare separators carries no fields. Holding them back would
	// charge them all against one event budget and eventually fail the stream.
	stream := strings.Repeat("\n", 4*(maxSSEEventBytes/3))
	var output strings.Builder

	if _, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(stream)); err != nil {
		t.Fatalf("blank separators must not exhaust the event budget: %v", err)
	}
	if output.String() != stream {
		t.Fatalf("separators were not forwarded: got %d of %d bytes", output.Len(), len(stream))
	}
}

func TestSSEStreamWriterAcceptsManyEventsInOneWrite(t *testing.T) {
	// The budget bounds a single event, not how much a caller hands over at
	// once. One write carrying more than the limit in small frames is legal.
	frame := "data: {\"n\":1}\n\n"
	stream := strings.Repeat(frame, (maxSSEEventBytes/len(frame))+16)
	events := 0
	writer := newSSEStreamWriter(func(serverSentEvent) error {
		events++
		return nil
	})

	if _, err := writer.Write([]byte(stream)); err != nil {
		t.Fatalf("a write carrying many small events must be accepted: %v", err)
	}
	if want := strings.Count(stream, frame); events != want {
		t.Fatalf("decoded %d events, want %d", events, want)
	}
}

func TestSSEDataRejectsMultipleJSONValuesInOneEvent(t *testing.T) {
	// Joined data lines that hold two values would be re-encoded as only the
	// first, silently dropping a delta the provider sent.
	stream := "data: {\"a\":1}\ndata: {\"b\":2}\n\n"
	var output strings.Builder

	_, err := copyNativeAnthropicStream(&output, strings.NewReader(stream), "gateway-model")
	if err == nil {
		t.Fatal("an event carrying two JSON values must fail the stream")
	}
	status, _ := statusAndCode(err)
	if status != 502 {
		t.Fatalf("status = %d, want 502", status)
	}
}

func TestSSEDataRejectsTrailingGarbageAfterJSON(t *testing.T) {
	// Decode stops at the end of the first value, so trailing bytes would vanish
	// when the frame is re-encoded.
	for _, payload := range []string{"{\"a\":1}]", "{\"a\":1} x", "{\"a\":1}{\"b\":2}"} {
		var output strings.Builder
		stream := "data: " + payload + "\n\n"

		if _, err := copyNativeAnthropicStream(&output, strings.NewReader(stream), "m"); err == nil {
			t.Fatalf("payload %q must fail the stream", payload)
		}
	}
}

func TestSSEStreamWriterReportsBytesConsumedOnFailure(t *testing.T) {
	// io.Writer callers may only assume the unreported tail had no effect. The
	// first frame here is handled before the second is refused.
	handled := 0
	writer := newSSEStreamWriter(func(serverSentEvent) error {
		handled++
		return nil
	})
	head := "data: {\"n\":1}\n\n"
	stream := head + "data: " + strings.Repeat("x", maxSSEEventBytes+1) + "\n\n"

	n, err := writer.Write([]byte(stream))
	if err == nil {
		t.Fatal("an oversized event must be rejected")
	}
	if handled != 1 {
		t.Fatalf("handled %d events, want the one that fit", handled)
	}
	if n != len(head) {
		t.Fatalf("reported %d bytes consumed, want %d", n, len(head))
	}
}

func TestSSEStreamWriterCloseDeliversNothingAfterAFailure(t *testing.T) {
	// The accepted lines of a rejected frame are still buffered. Close must not
	// hand them over: that would emit part of an event the writer refused.
	var delivered []serverSentEvent
	writer := newSSEStreamWriter(func(event serverSentEvent) error {
		delivered = append(delivered, event)
		return nil
	})
	if _, err := writer.Write([]byte("data: " + strings.Repeat("a", 4096) + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("data: " + strings.Repeat("b", maxSSEEventBytes) + "\n")); err == nil {
		t.Fatal("an oversized event must be rejected")
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close after a failure: %v", err)
	}
	if len(delivered) != 0 {
		t.Fatalf("Close delivered %d events from a rejected frame", len(delivered))
	}
	// Further writes stay refused rather than resuming mid-frame.
	if _, err := writer.Write([]byte("data: {}\n\n")); err == nil {
		t.Fatal("a failed writer must keep refusing writes")
	}
	if len(delivered) != 0 {
		t.Fatalf("a failed writer delivered %d events", len(delivered))
	}
}

func TestCopyOpenAIStreamReadsUsageFromSingleNewlineSeparatedChunks(t *testing.T) {
	// vLLM and Ollama OpenAI shims separate chunks with one newline instead of
	// the blank line SSE requires. Strict framing joins the whole stream into a
	// single frame, and billing must not silently drop to zero because of it.
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4,\"total_tokens\":16}}\n" +
		"data: [DONE]\n"
	var output strings.Builder

	usage, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != stream {
		t.Fatalf("stream changed during copy:\n%q", output.String())
	}
	want := Usage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("usage = %+v, want %+v", usage, want)
	}
}

func TestCopyNativeAnthropicStreamKeepsTheDataLineTerminator(t *testing.T) {
	// The rewritten data line has to be framed like the rest of the frame it
	// sits in; normalising CRLF to LF splits one frame across two conventions.
	stream := "event: message_start\r\n" +
		"data: {\"message\":{\"model\":\"upstream\"}}\r\n" +
		"\r\n" +
		"event: message_delta\n" +
		"data: {\"usage\":{\"output_tokens\":2}}\n" +
		"\n"
	var output strings.Builder

	if _, err := copyNativeAnthropicStream(&output, strings.NewReader(stream), "gateway-model"); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, "\"model\":\"gateway-model\"}}\r\n\r\n") {
		t.Fatalf("the CRLF frame lost its data line terminator:\n%q", got)
	}
	if !strings.Contains(got, "\"output_tokens\":2}}\n\n") {
		t.Fatalf("the LF frame lost its data line terminator:\n%q", got)
	}
	if strings.Contains(got, "\r\n\r\n\r") {
		t.Fatalf("a terminator was duplicated:\n%q", got)
	}
}

// heartbeatOnlyStream is a keepalive-only stream: SSE comments need no blank
// line, so an upstream may send these for minutes before its first real frame.
func heartbeatOnlyStream(count int) string {
	return strings.Repeat(": ping\n", count)
}

func TestCopyOpenAIStreamForwardsCommentHeartbeatsImmediately(t *testing.T) {
	// Held back, these never reach the client and it times out waiting. The
	// upstream here never closes, so only an immediate forward can satisfy this.
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	sink := &observingWriter{seen: make(chan string, 8)}

	go func() {
		_, _ = copyOpenAIStreamAndUsage(sink, reader)
	}()
	if _, err := writer.Write([]byte(": ping\n")); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-sink.seen:
		if got != ": ping\n" {
			t.Fatalf("forwarded %q, want the heartbeat", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the heartbeat was withheld from the client")
	}
	_ = writer.Close()
}

// observingWriter reports each write on a channel so a test can see a forwarded
// byte without waiting for the stream to end.
type observingWriter struct {
	seen chan string
}

func (w *observingWriter) Write(data []byte) (int, error) {
	w.seen <- string(data)
	return len(data), nil
}

func TestCopyOpenAIStreamDoesNotChargeCommentHeartbeatsToOneEvent(t *testing.T) {
	// A comment opens no frame, so a keepalive-only stream must never accumulate
	// toward the per-event limit.
	stream := heartbeatOnlyStream(3 * (maxSSEEventBytes / len(": ping\n")))
	var output strings.Builder

	if _, err := copyOpenAIStreamAndUsage(&output, strings.NewReader(stream)); err != nil {
		t.Fatalf("heartbeats must not exhaust the event budget: %v", err)
	}
	if output.String() != stream {
		t.Fatalf("heartbeats were not forwarded: got %d of %d bytes", output.Len(), len(stream))
	}
}

func TestSSEDecoderKeepsCommentsInsideAFrameWithTheFrame(t *testing.T) {
	// A comment between a frame's own lines belongs to that frame; splitting it
	// out would reorder the bytes a pass-through forwards.
	raw := "data: {\"a\":1}\n: mid-frame\ndata: extra\n\n"
	decoder := newSSEDecoder(strings.NewReader(raw))

	event, err := decoder.Next()
	if err != nil {
		t.Fatal(err)
	}
	if string(event.Raw) != raw {
		t.Fatalf("frame raw = %q, want the whole frame", event.Raw)
	}
	if event.Data != "{\"a\":1}\nextra" {
		t.Fatalf("data = %q, want both data lines joined", event.Data)
	}
	if _, err := decoder.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestAnthropicConverterIgnoresCommentHeartbeats(t *testing.T) {
	// The converter emits a translated stream, so an upstream comment is not
	// something it forwards; it must simply produce nothing.
	writer := &recordingWriter{}
	converter := newOpenAIAnthropicStreamConverter(writer, "gateway-model", 1, Provider{})

	if _, err := converter.Write([]byte(heartbeatOnlyStream(4))); err != nil {
		t.Fatal(err)
	}
	if writer.builder.Len() != 0 {
		t.Fatalf("converter forwarded a comment:\n%q", writer.builder.String())
	}
}

func TestCopyNativeAnthropicStreamForwardsFramesNeedingNoRewriteVerbatim(t *testing.T) {
	// Only message_start carries a model name. Re-encoding the others would
	// reorder their JSON keys and collapse their data lines for nothing.
	stream := "event: content_block_delta\r\n" +
		"data: {\"zebra\":1,\"alpha\":2,\r\n" +
		"data: \"index\":0}\r\n" +
		"\r\n" +
		"event: message_delta\n" +
		"data: {\"zebra\":3,\n" +
		"data: \"usage\":{\"output_tokens\":7}}\n" +
		"\n"
	var output strings.Builder

	usage, err := copyNativeAnthropicStream(&output, strings.NewReader(stream), "gateway-model")
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != stream {
		t.Fatalf("a frame with no model to replace was re-framed:\n%q", output.String())
	}
	if usage.CompletionTokens != 7 {
		t.Fatalf("usage = %+v, want 7 completion tokens", usage)
	}
}

func TestCopyNativeAnthropicStreamRewritesMultiLineModelFrame(t *testing.T) {
	// The one frame that does need rewriting may still arrive across several
	// data lines. It is re-framed onto one line, keeping that line's terminator.
	stream := "event: message_start\r\n" +
		"data: {\"message\":\r\n" +
		"data: {\"model\":\"upstream\",\"usage\":{\"input_tokens\":5}}}\r\n" +
		"\r\n"
	var output strings.Builder

	usage, err := copyNativeAnthropicStream(&output, strings.NewReader(stream), "gateway-model")
	if err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.HasPrefix(got, "event: message_start\r\n") {
		t.Fatalf("the event name was not preserved:\n%q", got)
	}
	if !strings.Contains(got, "\"model\":\"gateway-model\"") {
		t.Fatalf("the model was not rewritten:\n%q", got)
	}
	if strings.Contains(got, "upstream") {
		t.Fatalf("the upstream model name leaked:\n%q", got)
	}
	if !strings.HasSuffix(got, "\r\n\r\n") {
		t.Fatalf("the rewritten data line lost its CRLF terminator:\n%q", got)
	}
	if strings.Count(got, "data: ") != 1 {
		t.Fatalf("the rewritten payload should occupy one data line:\n%q", got)
	}
	if usage.PromptTokens != 5 {
		t.Fatalf("usage = %+v, want 5 prompt tokens", usage)
	}
}
