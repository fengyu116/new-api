package helper

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

type ImageDataURL struct {
	MIMEType  string
	Extension string
	payload   string
}

func HasImageReferences(request dto.ImageRequest) bool {
	return hasRawJSONValue(request.Images) || hasRawJSONValue(request.Image)
}

func HasImageDataURLReferences(request dto.ImageRequest) bool {
	references, err := ImageReferences(request)
	if err != nil {
		return false
	}
	for _, reference := range references {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(reference)), "data:") {
			return true
		}
	}
	return false
}

func HasJSONImageDataURLReferences(data []byte) bool {
	references, err := JSONImageReferences(data)
	if err != nil {
		return false
	}
	for _, reference := range references {
		if IsImageDataURL(reference) {
			return true
		}
	}
	return false
}

func JSONImageReferences(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	keys := []string{"image", "images", "image[]", "input_reference", "reference", "references", "image_prompt"}
	references := make([]string, 0)
	for _, key := range keys {
		raw, ok := fields[key]
		if !ok || !hasRawJSONValue(raw) {
			continue
		}
		values, err := rawStringOrStringArray(raw)
		if err != nil {
			return nil, fmt.Errorf("%s must be a string or array of strings", key)
		}
		references = append(references, values...)
	}
	return references, nil
}

func ImageReferences(request dto.ImageRequest) ([]string, error) {
	raw := request.Images
	if !hasRawJSONValue(raw) {
		raw = request.Image
	}
	if !hasRawJSONValue(raw) {
		return nil, errors.New("image is required")
	}

	var images []string
	if err := common.Unmarshal(raw, &images); err == nil {
		if len(images) == 0 {
			return nil, errors.New("image is required")
		}
		for index, image := range images {
			if strings.TrimSpace(image) == "" {
				return nil, fmt.Errorf("image %d is empty", index+1)
			}
		}
		return images, nil
	}

	var image string
	if err := common.Unmarshal(raw, &image); err != nil {
		return nil, errors.New("image must be a string or images must be an array of strings")
	}
	if strings.TrimSpace(image) == "" {
		return nil, errors.New("image is required")
	}
	return []string{image}, nil
}

func ParseImageDataURL(value string) (*ImageDataURL, error) {
	trimmed := strings.TrimSpace(value)
	comma := strings.IndexByte(trimmed, ',')
	if comma <= len("data:") || !strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		return nil, errors.New("only base64 data URLs are supported")
	}

	header := trimmed[len("data:"):comma]
	headerParts := strings.Split(header, ";")
	mimeType := strings.ToLower(strings.TrimSpace(headerParts[0]))
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, fmt.Errorf("unsupported media type %q", mimeType)
	}
	isBase64 := false
	for _, part := range headerParts[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			isBase64 = true
			break
		}
	}
	if !isBase64 {
		return nil, errors.New("data URL must use base64 encoding")
	}

	payload := trimmed[comma+1:]
	if payload == "" {
		return nil, errors.New("base64 payload is empty")
	}
	return &ImageDataURL{
		MIMEType:  mimeType,
		Extension: imageExtensionForMIME(mimeType),
		payload:   payload,
	}, nil
}

func (d *ImageDataURL) Decoder() io.Reader {
	return base64.NewDecoder(base64.StdEncoding, strings.NewReader(d.payload))
}

func (d *ImageDataURL) Base64Payload() string {
	return d.payload
}

func WriteImageDataURLFile(writer *multipart.Writer, fieldName string, filenamePrefix string, dataURL string) error {
	parsed, err := ParseImageDataURL(dataURL)
	if err != nil {
		return err
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s.%s"`, fieldName, filenamePrefix, parsed.Extension))
	partHeader.Set("Content-Type", parsed.MIMEType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return fmt.Errorf("failed to create multipart file: %w", err)
	}
	if _, err := io.Copy(part, parsed.Decoder()); err != nil {
		return fmt.Errorf("failed to decode base64 payload: %w", err)
	}
	return nil
}

func IsImageDataURL(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "data:image/")
}

func rawStringOrStringArray(raw json.RawMessage) ([]string, error) {
	var values []string
	if err := common.Unmarshal(raw, &values); err == nil {
		return nonEmptyStrings(values), nil
	}
	var value string
	if err := common.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return nonEmptyStrings([]string{value}), nil
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func hasRawJSONValue(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "[]"
}

func imageExtensionForMIME(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/bmp":
		return "bmp"
	default:
		return "png"
	}
}
