package mailparse_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/isseis/tlsrpt-digest/internal/mailparse"
)

// buildMsg creates a *mail.Message from a raw RFC 5322 string.
func buildMsg(t *testing.T, raw string) *mail.Message {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	require.NoError(t, err)
	return msg
}

// b64 base64-encodes s.
func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

// buildDeeplyNestedMsg builds a multipart/mixed email with the given number of nested multipart layers,
// each wrapping a text/plain leaf at the innermost level.
func buildDeeplyNestedMsg(t *testing.T, depth int) *mail.Message {
	t.Helper()
	// Build from the inside out.
	// innermost body is text/plain.
	inner := "Content-Type: text/plain\r\n\r\nhello"

	for i := range depth {
		boundary := fmt.Sprintf("b%d", i)
		inner = fmt.Sprintf(
			"Content-Type: multipart/mixed; boundary=%q\r\n\r\n--%s\r\n%s\r\n--%s--",
			boundary, boundary, inner, boundary,
		)
	}

	raw := "MIME-Version: 1.0\r\n" + inner
	return buildMsg(t, raw)
}

func TestExtractAttachments(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		maxBytes  int64
		wantCount int
		wantNames []string
		wantErr   bool
		wantErrIs error
	}{
		{
			name: "content_disposition_attachment",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"file.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("hello") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"file.bin"},
		},
		{
			name: "no_disposition_with_name",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream; name=\"data.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("data") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"data.bin"},
		},
		{
			name: "inline_with_name_skipped",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: text/plain; name=\"inline.txt\"\r\n" +
				"Content-Disposition: inline\r\n\r\n" +
				"hello\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 0,
		},
		{
			name: "multipart_mixed_attachment",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				"hello\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"a.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("aaa") + "\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"b.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("bbb") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 2,
			wantNames: []string{"a.bin", "b.bin"},
		},
		{
			name: "nested_multipart",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"outer\"\r\n\r\n" +
				"--outer\r\n" +
				"Content-Type: multipart/mixed; boundary=\"inner\"\r\n\r\n" +
				"--inner\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"nested.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("nested") + "\r\n" +
				"--inner--\r\n" +
				"--outer--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"nested.bin"},
		},
		{
			name: "toplevel_non_multipart_attachment",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: application/octet-stream; name=\"top.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("topdata"),
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"top.bin"},
		},
		{
			name: "plaintext_returns_empty",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				"Hello world",
			maxBytes:  1 << 20,
			wantCount: 0,
		},
		{
			name: "base64_decoded",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"f.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("decoded-content") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
		},
		{
			name: "base64_decode_failure_skipped",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"bad.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				"!!!not-valid-base64!!!\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 0,
		},
		{
			name: "filename_from_disposition",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream; name=\"type-name.bin\"\r\n" +
				"Content-Disposition: attachment; filename=\"disp-name.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("x") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"disp-name.bin"},
		},
		{
			name: "filename_from_content_type",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream; name=\"type-name.bin\"\r\n" +
				"Content-Disposition: attachment\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("x") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"type-name.bin"},
		},
		{
			name: "filename_empty",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("x") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{""},
		},
		{
			name: "rfc2231_filename",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename*=UTF-8''test%21file.bin\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("x") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"test!file.bin"},
		},
		{
			name: "no_attachments_empty_slice",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: text/plain\r\n\r\n" +
				"no attachments here\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 0,
		},
		{
			name: "invalid_content_type_error",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: /invalid\r\n\r\n" +
				"body",
			maxBytes: 1 << 20,
			wantErr:  true,
		},
		{
			name: "invalid_boundary_error",
			// Part header has no colon — textproto returns ProtocolError.
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"badheadernocoion\r\n" +
				"\r\n" +
				"body\r\n" +
				"--b--",
			maxBytes: 1 << 20,
			wantErr:  true,
		},
		{
			name: "rfc2047_filename",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"=?UTF-8?B?dGVzdC5iaW4=?=\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("x") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 1,
			wantNames: []string{"test.bin"},
		},
		{
			// Content-Type is required for a part to be extracted.
			// A missing Content-Type causes the part to be skipped even if
			// Content-Disposition: attachment is present.
			name: "missing_content_type_skipped",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Disposition: attachment; filename=\"f.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("hello") + "\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 0,
		},
		{
			name: "non_base64_encoding_skipped",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"qp.bin\"\r\n" +
				"Content-Transfer-Encoding: quoted-printable\r\n\r\n" +
				"hello=3D\r\n" +
				"--b--",
			maxBytes:  1 << 20,
			wantCount: 0,
		},
		{
			name: "size_limit_exceeded",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"big.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("this content is longer than one byte") + "\r\n" +
				"--b--",
			maxBytes: 1,
			wantErr:  true,
		},
		{
			name: "zero_max_bytes_no_limit",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"f.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("large content here") + "\r\n" +
				"--b--",
			maxBytes:  0,
			wantCount: 1,
		},
		{
			name: "negative_max_bytes_no_limit",
			raw: "MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
				"--b\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: attachment; filename=\"f.bin\"\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				b64("large content here") + "\r\n" +
				"--b--",
			maxBytes:  -1,
			wantCount: 1,
		},
		{
			name: "nesting_too_deep",
			// built separately below
			wantErr:   true,
			wantErrIs: mailparse.ErrMIMETooDeep,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var msg *mail.Message
			if tc.name == "nesting_too_deep" {
				msg = buildDeeplyNestedMsg(t, 12)
			} else {
				msg = buildMsg(t, tc.raw)
			}

			got, err := mailparse.ExtractAttachments(msg, tc.maxBytes)

			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrIs != nil {
					require.True(t, errors.Is(err, tc.wantErrIs))
				}
				return
			}

			require.NoError(t, err)
			require.Len(t, got, tc.wantCount)

			if tc.wantNames != nil {
				for i, name := range tc.wantNames {
					require.Equal(t, name, got[i].Filename)
				}
			}
		})
	}
}

func TestExtractAttachments_ErrSizeLimitExceeded_Fields(t *testing.T) {
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"f.bin\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64("hello world") + "\r\n" +
		"--b--"

	msg := buildMsg(t, raw)
	_, err := mailparse.ExtractAttachments(msg, 5)
	require.Error(t, err)

	var sizeErr *mailparse.ErrSizeLimitExceeded
	require.True(t, errors.As(err, &sizeErr))
	require.Equal(t, int64(5), sizeErr.Limit)
	require.Greater(t, sizeErr.Actual, int64(5))
}

func TestExtractAttachments_base64_content(t *testing.T) {
	want := "decoded-content"
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"f.bin\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64(want) + "\r\n" +
		"--b--"

	msg := buildMsg(t, raw)
	got, err := mailparse.ExtractAttachments(msg, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, []byte(want), got[0].Content)
}

// Security tests

func TestExtractAttachments_Security_PathTraversalFilenamePassedThrough(t *testing.T) {
	raw := "MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"../etc/passwd\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		b64("data") + "\r\n" +
		"--b--"

	msg := buildMsg(t, raw)
	got, err := mailparse.ExtractAttachments(msg, 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	// The package does NOT sanitize filenames; caller is responsible.
	require.Equal(t, "../etc/passwd", got[0].Filename)
}

// Integration test

func TestExtractAttachments_Integration(t *testing.T) {
	data, err := os.ReadFile("../../testdata/tlsrpt_google.eml")
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(data))
	require.NoError(t, err)

	attachments, err := mailparse.ExtractAttachments(msg, 10<<20)
	require.NoError(t, err)
	require.Len(t, attachments, 1)

	att := attachments[0]
	require.Contains(t, att.Filename, ".json.gz")
	require.Greater(t, len(att.Content), 0)
	// Verify gzip magic bytes
	require.Equal(t, byte(0x1f), att.Content[0])
	require.Equal(t, byte(0x8b), att.Content[1])
}
