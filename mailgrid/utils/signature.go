package utils

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// maxSignatureBytes caps the signature text file size (1 MB is generous for
// a plain-text footer; it also keeps malformed binary files out).
const maxSignatureBytes = 1 << 20

// ReadSignatureFile reads the signature text file and returns its content.
// Windows Notepad may save as UTF-8 (with or without BOM) or legacy ANSI/GBK
// depending on version and settings, so the encoding is detected per file:
// BOM wins, then strict UTF-8, then GBK as the fallback.
func ReadSignatureFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) > maxSignatureBytes {
		return "", fmt.Errorf("signature file too large (>%d bytes): %s", maxSignatureBytes, path)
	}

	var text string
	switch {
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		text = string(raw[3:]) // UTF-8 BOM
	case utf8.Valid(raw):
		text = string(raw)
	default:
		decoded, decErr := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
		if decErr != nil {
			return "", fmt.Errorf("signature file is neither UTF-8 nor GBK: %s", path)
		}
		text = string(decoded)
	}
	return text, nil
}

// SignatureToHTML converts plain-text signature lines into an HTML footer
// block. Empty lines are preserved as spacing; content is HTML-escaped.
func SignatureToHTML(text string) string {
	text = strings.Trim(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var sb strings.Builder
	sb.WriteString(`<div style="margin-top:28px;padding-top:12px;border-top:1px solid #dddddd;color:#666666;font-size:12px;line-height:1.6;">`)
	for _, line := range lines {
		sb.WriteString(html.EscapeString(line))
		sb.WriteString("<br>\n")
	}
	sb.WriteString("</div>")
	return sb.String()
}
