//go:build !wasm

package runtime

import (
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// MultipartParse parses multipart form body and returns fields and files.
func MultipartParse(req interface{}) interface{} {
	var body string
	var contentType string

	switch r := req.(type) {
	case Request:
		body = r.Body
		contentType = r.Headers["content-type"]
		if contentType == "" {
			contentType = r.Headers["Content-Type"]
		}
	case map[string]interface{}:
		if b, ok := r["body"].(string); ok {
			body = b
		}
		if headers, ok := r["headers"].(map[string]interface{}); ok {
			if ct, ok := headers["content-type"].(string); ok {
				contentType = ct
			} else if ct, ok := headers["Content-Type"].(string); ok {
				contentType = ct
			}
		} else if headers, ok := r["headers"].(map[string]string); ok {
			contentType = headers["content-type"]
			if contentType == "" {
				contentType = headers["Content-Type"]
			}
		}
	default:
		return map[string]interface{}{
			"fields": map[string]interface{}{},
			"files":  []interface{}{},
		}
	}

	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}

	boundary, ok := params["boundary"]
	if !ok {
		return [2]interface{}{nil, "no multipart boundary found"}
	}

	mr := multipart.NewReader(strings.NewReader(body), boundary)
	fields := make(map[string]interface{})
	var files []interface{}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return [2]interface{}{nil, err.Error()}
		}

		content, err := io.ReadAll(part)
		if err != nil {
			return [2]interface{}{nil, err.Error()}
		}

		if part.FileName() == "" {
			fields[part.FormName()] = string(content)
		} else {
			files = append(files, map[string]interface{}{
				"name":     part.FormName(),
				"filename": part.FileName(),
				"size":     float64(len(content)),
				"content":  string(content),
			})
		}
	}

	return map[string]interface{}{
		"fields": fields,
		"files":  files,
	}
}
