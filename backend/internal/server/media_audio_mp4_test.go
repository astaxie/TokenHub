package server

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func transcriptionMP4FixtureBox(kind string, payload []byte, extended bool) []byte {
	headerSize := 8
	if extended {
		headerSize = 16
	}
	box := make([]byte, headerSize+len(payload))
	copy(box[4:8], kind)
	if extended {
		binary.BigEndian.PutUint32(box[:4], 1)
		binary.BigEndian.PutUint64(box[8:16], uint64(len(box)))
	} else {
		binary.BigEndian.PutUint32(box[:4], uint32(len(box)))
	}
	copy(box[headerSize:], payload)
	return box
}

func TestTranscriptionMP4ContainerBoxSignatures(t *testing.T) {
	brand := []byte("M4A \x00\x00\x00\x00")
	ftyp := transcriptionMP4FixtureBox("ftyp", brand, false)
	free := transcriptionMP4FixtureBox("free", nil, false)
	skip := transcriptionMP4FixtureBox("skip", []byte("padding"), true)
	declaredBox := func(size uint64, extended bool) []byte {
		box := transcriptionMP4FixtureBox("free", nil, extended)
		if extended {
			binary.BigEndian.PutUint64(box[8:16], size)
		} else {
			binary.BigEndian.PutUint32(box[:4], uint32(size))
		}
		return append(box, ftyp...)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"initial ftyp", ftyp, true},
		{"leading free", append(free, ftyp...), true},
		{"leading extended skip", append(skip, ftyp...), true},
		{"mixed padding", append(append(free, skip...), ftyp...), true},
		{"extended ftyp", transcriptionMP4FixtureBox("ftyp", brand, true), true},
		{"last inspected box", append(bytes.Repeat(free, 127), ftyp...), true},
		{"box inspection limit", append(bytes.Repeat(free, 128), ftyp...), false},
		{"truncated header", free[:7], false},
		{"truncated extended header", skip[:15], false},
		{"zero size", declaredBox(0, false), false},
		{"short size", declaredBox(7, false), false},
		{"oversized size", declaredBox(100, false), false},
		{"zero extended size", declaredBox(0, true), false},
		{"short extended size", declaredBox(15, true), false},
		{"oversized extended size", declaredBox(100, true), false},
		{"overflow extended size", declaredBox(^uint64(0), true), false},
		{"truncated ftyp", ftyp[:len(ftyp)-1], false},
		{"short ftyp body", transcriptionMP4FixtureBox("ftyp", brand[:4], false), false},
		{"unsupported leading box", append(transcriptionMP4FixtureBox("moov", nil, false), ftyp...), false},
		{"ftyp inside padding", transcriptionMP4FixtureBox("free", ftyp, false), false},
		{"missing ftyp", free, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, filename := range []string{"sample.mp4", "sample.m4a"} {
				if got := transcriptionAudioSignatureMatches(filename, tc.data); got != tc.want {
					t.Errorf("%s signature = %v, want %v", filename, got, tc.want)
				}
			}
		})
	}
}

func TestTranscriptionMultipartForwardsMP4WithLeadingPadding(t *testing.T) {
	for _, filename := range []string{"sample.mp4", "sample.m4a"} {
		t.Run(filename, func(t *testing.T) {
			audio := transcriptionMP4FixtureBox("free", nil, false)
			audio = append(audio, transcriptionMP4FixtureBox("skip", []byte("padding"), true)...)
			audio = append(audio, transcriptionMP4FixtureBox("ftyp", []byte("M4A \x00\x00\x00\x00"), false)...)
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer func() { _ = r.MultipartForm.RemoveAll() }()
				file, header, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer file.Close()
				data, err := io.ReadAll(file)
				if err != nil || header.Filename != filename || !bytes.Equal(data, audio) {
					t.Errorf("uploaded container changed: filename=%s bytes=%d error=%v", header.Filename, len(data), err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"text":"fixture transcript"}`)
			}, "audio")
			response := httptest.NewRecorder()
			file := transcriptionFixtureFile("file", filename, "audio/mp4", audio)
			server.Handler().ServeHTTP(response, transcriptionFixtureRequest(t, key, [][2]string{{"model", "public-media"}}, []mediaFile{file}))
			if response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("padded container did not reach upstream: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}
