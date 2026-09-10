package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type nativeCodexImageEditRequest struct {
	Model          string                  `json:"model"`
	Prompt         string                  `json:"prompt"`
	Images         []nativeCodexImageInput `json:"images"`
	N              int                     `json:"n,omitempty"`
	Quality        string                  `json:"quality,omitempty"`
	Size           string                  `json:"size,omitempty"`
	ResponseFormat string                  `json:"response_format,omitempty"`
}

type nativeCodexImageInput struct {
	ImageURL string `json:"image_url"`
}

func (s *Server) isNativeImageEditRequest(r *http.Request) bool {
	for _, profile := range s.providerImageCapabilityRouteProfiles() {
		profile.withDefaults()
		if profile.RequestAliasModel != "" && imageGenerationRequestAliasMatches(r, profile) {
			return true
		}
	}
	return false
}

func decodeNativeCodexImageEdit(r *http.Request) (imageGenerationRequest, []uploadedImage, error) {
	var payload nativeCodexImageEditRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&payload); err != nil {
		return imageGenerationRequest{}, nil, nativeCodexImageEditDecodeError(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return imageGenerationRequest{}, nil, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", "request body must contain a single JSON value")
		}
		return imageGenerationRequest{}, nil, nativeCodexImageEditDecodeError(err)
	}

	inputs := make([]uploadedImage, 0, len(payload.Images))
	for _, image := range payload.Images {
		_, encoded, err := parseDataURI(strings.TrimSpace(image.ImageURL))
		if err != nil {
			return imageGenerationRequest{}, nil, NewHTTPError(http.StatusBadRequest, "invalid_input_image", AsHTTPError(err).Message)
		}
		data, err := readUploadedImagePart(base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
		if err != nil {
			return imageGenerationRequest{}, nil, err
		}
		inputs = append(inputs, uploadedImage{role: "input", data: data})
	}

	return imageGenerationRequest{
		Model: payload.Model, Prompt: payload.Prompt, N: payload.N, Quality: payload.Quality,
		Size: payload.Size, ResponseFormat: payload.ResponseFormat,
	}, inputs, nil
}

func nativeCodexImageEditDecodeError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return NewHTTPError(http.StatusRequestEntityTooLarge, "image_edit_request_too_large", "Image edit JSON requests must be 128 MB or smaller")
	}
	return NewHTTPError(http.StatusBadRequest, "invalid_image_edit", err.Error())
}

func decodeMultipartImageEdit(r *http.Request) (imageGenerationRequest, []uploadedImage, *uploadedImage, error) {
	multipartReader, err := r.MultipartReader()
	if err != nil {
		return imageGenerationRequest{}, nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", err.Error())
	}
	fields := make(map[string]string)
	inputs := make([]uploadedImage, 0, 1)
	var mask *uploadedImage
	for {
		part, partErr := multipartReader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			return imageGenerationRequest{}, nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", partErr.Error())
		}
		name := part.FormName()
		switch name {
		case "image", "image[]":
			data, readErr := readUploadedImagePart(part)
			_ = part.Close()
			if readErr != nil {
				return imageGenerationRequest{}, nil, nil, readErr
			}
			inputs = append(inputs, uploadedImage{role: "input", data: data})
		case "mask":
			data, readErr := readUploadedImagePart(part)
			_ = part.Close()
			if readErr != nil {
				return imageGenerationRequest{}, nil, nil, readErr
			}
			if mask != nil {
				return imageGenerationRequest{}, nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", "Only one mask is supported")
			}
			mask = &uploadedImage{role: "mask", data: data}
		default:
			data, readErr := io.ReadAll(io.LimitReader(part, maxImageTextFieldBytes+1))
			_ = part.Close()
			if readErr != nil || len(data) > maxImageTextFieldBytes {
				return imageGenerationRequest{}, nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_image_edit", "Multipart text field is too large or unreadable")
			}
			fields[name] = string(data)
		}
	}

	request := imageGenerationRequest{
		Model: fields["model"], Prompt: fields["prompt"], Quality: fields["quality"],
		Size: fields["size"], ResponseFormat: fields["response_format"],
	}
	if rawN := strings.TrimSpace(fields["n"]); rawN != "" {
		request.N, err = strconv.Atoi(rawN)
		if err != nil {
			return imageGenerationRequest{}, nil, nil, NewHTTPError(http.StatusBadRequest, "invalid_image_count", "n must be an integer")
		}
	}
	return request, inputs, mask, nil
}
