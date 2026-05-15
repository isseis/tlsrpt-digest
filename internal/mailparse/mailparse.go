// Package mailparse extracts attachments from MIME mail messages.
package mailparse

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
)

// Attachment represents a single attachment extracted from a mail message.
type Attachment struct {
	Filename    string
	ContentType string // MIME media type, e.g. "application/tlsrpt+gzip"
	Content     []byte
}

// ErrSizeLimitExceeded is returned when the cumulative decoded size of attachments exceeds the limit.
type ErrSizeLimitExceeded struct {
	Limit  int64
	Actual int64
}

func (e *ErrSizeLimitExceeded) Error() string {
	return fmt.Sprintf("mailparse: size limit exceeded: limit=%d actual=%d", e.Limit, e.Actual)
}

// ErrMIMETooDeep is returned when multipart nesting depth exceeds the limit.
var ErrMIMETooDeep = errors.New("mailparse: multipart nesting too deep")

const maxMultipartDepth = 10

// ExtractAttachments extracts all attachments from a mail message.
// maxBytes is the cumulative decoded size limit; pass 0 or negative for no limit.
func ExtractAttachments(msg *mail.Message, maxBytes int64) ([]Attachment, error) {
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		return nil, nil
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("mailparse: parse content-type: %w", err)
	}

	var accumulated int64
	if strings.HasPrefix(mediaType, "multipart/") {
		return extractParts(msg.Body, params["boundary"], 0, maxBytes, &accumulated)
	}

	// Top-level non-multipart: check if it qualifies as an attachment.
	disp, dispParams, _ := mime.ParseMediaType(msg.Header.Get("Content-Disposition"))
	if !isAttachment(disp, params) {
		return nil, nil
	}

	enc := msg.Header.Get("Content-Transfer-Encoding")
	content, err := decodeContent(msg.Body, enc, maxBytes, &accumulated)
	if err != nil {
		if _, ok := errors.AsType[*ErrSizeLimitExceeded](err); ok {
			return nil, err
		}
		// decode failure: skip
		return nil, nil
	}

	filename := resolveFilename(dispParams, params)
	return []Attachment{{Filename: filename, ContentType: mediaType, Content: content}}, nil
}

func extractParts(r io.Reader, boundary string, depth int, maxBytes int64, accumulated *int64) ([]Attachment, error) {
	if depth > maxMultipartDepth {
		return nil, fmt.Errorf("%w: depth=%d limit=%d", ErrMIMETooDeep, depth, maxMultipartDepth)
	}

	mr := multipart.NewReader(r, boundary)
	var results []Attachment

	for {
		part, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("mailparse: parse multipart: %w", err)
		}

		attachments, partErr := processPart(part, depth, maxBytes, accumulated)
		if partErr != nil {
			return nil, partErr
		}
		results = append(results, attachments...)
	}

	return results, nil
}

func processPart(part *multipart.Part, depth int, maxBytes int64, accumulated *int64) ([]Attachment, error) {
	defer func() {
		_ = part.Close()
	}()

	contentType := part.Header.Get("Content-Type")
	if contentType == "" {
		// Keep existing behavior: skip parts without Content-Type.
		return nil, nil
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		// Skip unparseable part content-type.
		return nil, nil
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		nested, err := extractParts(part, params["boundary"], depth+1, maxBytes, accumulated)
		if err != nil {
			return nil, err
		}
		return nested, nil
	}

	disp, dispParams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if !isAttachment(disp, params) {
		return nil, nil
	}

	enc := part.Header.Get("Content-Transfer-Encoding")
	content, err := decodeContent(part, enc, maxBytes, accumulated)
	if err != nil {
		if _, ok := errors.AsType[*ErrSizeLimitExceeded](err); ok {
			return nil, err
		}
		// base64 decode failure: skip
		return nil, nil
	}

	filename := resolveFilename(dispParams, params)
	return []Attachment{{Filename: filename, ContentType: mediaType, Content: content}}, nil
}

// isAttachment returns true if the part should be treated as an attachment.
func isAttachment(disp string, contentTypeParams map[string]string) bool {
	switch disp {
	case "attachment":
		return true
	case "inline":
		return false
	default:
		// No Content-Disposition: treat as attachment only if Content-Type has a name parameter.
		_, hasName := contentTypeParams["name"]
		return hasName
	}
}

// resolveFilename returns the filename for an attachment, preferring Content-Disposition filename
// over Content-Type name, falling back to empty string.
func resolveFilename(dispParams map[string]string, contentTypeParams map[string]string) string {
	var raw string
	if fn, ok := dispParams["filename"]; ok {
		raw = fn
	} else if name, ok := contentTypeParams["name"]; ok {
		raw = name
	}
	if raw == "" {
		return ""
	}

	// RFC 2047 decode
	dec := mime.WordDecoder{}
	decoded, err := dec.DecodeHeader(raw)
	if err != nil {
		return raw
	}
	return decoded
}

// decodeContent reads and decodes the content of a part.
// For base64 encoding it streams the decode and tracks cumulative size.
// For other encodings, it skips (returns errSkip).
func decodeContent(r io.Reader, enc string, maxBytes int64, accumulated *int64) ([]byte, error) {
	encLower := strings.ToLower(strings.TrimSpace(enc))
	if encLower != "base64" && encLower != "" {
		return nil, errSkip
	}

	var src io.Reader
	if encLower == "base64" {
		src = base64.NewDecoder(base64.StdEncoding, r)
	} else {
		src = r
	}

	const chunkSize = 32 * 1024
	buf := make([]byte, chunkSize)
	var content []byte

	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			*accumulated += int64(n)
			if maxBytes > 0 && *accumulated > maxBytes {
				return nil, &ErrSizeLimitExceeded{Limit: maxBytes, Actual: *accumulated}
			}
			content = append(content, buf[:n]...)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}

	return content, nil
}

// errSkip is an internal sentinel used to skip parts with unsupported encodings.
var errSkip = errors.New("mailparse: skip")
