package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
)

// Media fields remain opaque so vendor extensions and large integer IDs survive.
// Files are bounded separately and are never placed in request audit payloads.
type mediaRequest struct {
	Fields    map[string]json.RawMessage
	Files     []mediaFile
	Multipart bool
}
type mediaFile struct {
	Header textproto.MIMEHeader
	Data   []byte
}

func (m mediaRequest) model() string {
	var v string
	_ = json.Unmarshal(m.Fields["model"], &v)
	return strings.TrimSpace(v)
}
func (m mediaRequest) stream() bool {
	var value bool
	if m.Multipart {
		var text string
		_ = json.Unmarshal(m.Fields["stream"], &text)
		value, _ = strconv.ParseBool(text)
	} else {
		_ = json.Unmarshal(m.Fields["stream"], &value)
	}
	return value
}

func decodeMediaRequest(w http.ResponseWriter, r *http.Request, limit int64) (mediaRequest, error) {
	request := mediaRequest{Fields: map[string]json.RawMessage{}}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return request, NewHTTPError(415, "invalid_content_type", "Use application/json or multipart/form-data")
	}
	switch contentType {
	case "application/json":
		decoder := json.NewDecoder(r.Body)
		err = decoder.Decode(&request.Fields)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = errors.New("expected a single JSON object")
			}
		}
	case "multipart/form-data":
		request.Multipart = true
		reader := multipart.NewReader(r.Body, params["boundary"])
		fields := map[string][]string{}
		for count := 0; ; count++ {
			var part *multipart.Part
			part, err = reader.NextPart()
			if err == io.EOF {
				err = nil
				break
			}
			if err != nil {
				break
			}
			if count >= 128 || part.FormName() == "" {
				err = errors.New("invalid multipart fields")
				break
			}
			var data []byte
			data, err = io.ReadAll(part)
			if err != nil {
				break
			}
			if part.FileName() != "" {
				if part.FormName() == "model" {
					err = errors.New("model must be a text field")
					break
				}
				request.Files = append(request.Files, mediaFile{Header: part.Header, Data: data})
			} else {
				if len(data) > maxImageTextFieldBytes {
					err = errors.New("text field is too large")
					break
				}
				fields[part.FormName()] = append(fields[part.FormName()], string(data))
			}
		}
		if err == nil {
			for name, values := range fields {
				if len(values) == 1 {
					request.Fields[name], _ = json.Marshal(values[0])
				} else if name == "model" {
					err = errors.New("model must occur exactly once")
				} else {
					request.Fields[name], _ = json.Marshal(values)
				}
			}
		}
	default:
		return request, NewHTTPError(415, "invalid_content_type", "Use application/json or multipart/form-data")
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return request, NewHTTPError(413, "request_too_large", "Media request exceeds the configured request size limit")
		}
		return request, NewHTTPError(400, "invalid_media_request", "Invalid JSON or multipart media request")
	}
	if request.model() == "" {
		return request, NewHTTPError(400, "missing_model", "model is required")
	}
	return request, nil
}

func (m mediaRequest) encode(model string) ([]byte, string, error) {
	fields := cloneRawJSON(m.Fields, 1)
	setRawJSONField(fields, "model", model, true)
	if !m.Multipart {
		data, err := json.Marshal(fields)
		return data, "application/json", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, raw := range fields {
		var values []string
		var value string
		if json.Unmarshal(raw, &value) == nil {
			values = []string{value}
		} else if json.Unmarshal(raw, &values) != nil {
			return nil, "", NewHTTPError(400, "invalid_media_field", "Multipart media fields must be strings or arrays of strings")
		}
		for _, value := range values {
			if err := writer.WriteField(name, value); err != nil {
				return nil, "", err
			}
		}
	}
	for _, file := range m.Files {
		part, err := writer.CreatePart(file.Header)
		if err != nil {
			return nil, "", err
		}
		if _, err = part.Write(file.Data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}
