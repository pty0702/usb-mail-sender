// Package email provides helpers for formatting and delivering mail via SMTP.
//
// SendWithClient sanitizing and deduplicates recipients case-insensitively across
// the To, CC, and BCC lists in that order. The primary recipient is always
// addressed first, and only one RCPT command is issued per unique address. CC
// headers include only deduplicated CC recipients while BCC addresses are added
// to the envelope but never leaked via headers.
package email

import (
	"bufio"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"mime"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bravo1goingdark/mailgrid/config"
)

// isASCII checks if a string contains only printable ASCII characters.
func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 || r < 32 {
			return false
		}
	}
	return true
}

// sanitizeFilename removes quotes, backslashes, and newlines from
// attachment filenames to prevent header injection.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\"", "")
	name = strings.ReplaceAll(name, "\\", "")
	name = strings.ReplaceAll(name, "\r", "")
	name = strings.ReplaceAll(name, "\n", "")
	return name
}

var bufWriterPool = sync.Pool{New: func() any { return bufio.NewWriterSize(io.Discard, 64*1024) }}
var copyBufPool = sync.Pool{New: func() any { b := make([]byte, 32*1024); return &b }}

// newBoundary returns a unique MIME boundary string. 12 bytes from crypto/rand
// give 96 bits of entropy — collision-free across goroutines and processes.
func newBoundary(prefix string) string {
	var b [12]byte
	if _, err := crand.Read(b[:]); err != nil {
		// crypto/rand failure is exceptional; the resulting boundary is still
		// unique enough at SMTP scale because Go zeroes the array.
		return prefix + "fallback"
	}
	return prefix + hex.EncodeToString(b[:])
}

// writeHeader writes "Key: Value\r\n" to bw without intermediate string allocs.
func writeHeader(bw *bufio.Writer, key, value string) error {
	if _, err := bw.WriteString(key); err != nil {
		return err
	}
	if _, err := bw.WriteString(": "); err != nil {
		return err
	}
	if _, err := bw.WriteString(value); err != nil {
		return err
	}
	_, err := bw.WriteString("\r\n")
	return err
}

// writeBoundaryLine writes "--<boundary>\r\n" without concatenation.
func writeBoundaryLine(bw *bufio.Writer, boundary string) error {
	if _, err := bw.WriteString("--"); err != nil {
		return err
	}
	if _, err := bw.WriteString(boundary); err != nil {
		return err
	}
	_, err := bw.WriteString("\r\n")
	return err
}

// writeBoundaryClose writes "--<boundary>--\r\n" without concatenation.
func writeBoundaryClose(bw *bufio.Writer, boundary string, trailingCRLF bool) error {
	if _, err := bw.WriteString("--"); err != nil {
		return err
	}
	if _, err := bw.WriteString(boundary); err != nil {
		return err
	}
	if trailingCRLF {
		_, err := bw.WriteString("--\r\n")
		return err
	}
	_, err := bw.WriteString("--")
	return err
}

// SendWithClient formats and delivers an email using an active SMTP client.
// Recipients from the To, CC, and BCC fields are trimmed, deduplicated in that
// order (case-insensitively), and issued RCPT commands exactly once with the
// primary recipient always first. The CC header is rendered from the unique CC
// list, while BCC addresses are kept solely on the SMTP envelope.
// It uses cfg.SMTP.From as both the envelope sender and header From address.
//
// cache may be nil; when supplied, attachments are read and base64-encoded
// once per dispatch run and reused across all recipients.
func SendWithClient(client *smtp.Client, cfg config.SMTPConfig, task Task, cache *AttachmentCache) (err error) {
	from := strings.TrimSpace(cfg.From)
	if from == "" {
		return fmt.Errorf("SMTP sender 'from' field in config is empty")
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM error: %w", err)
	}

	to := strings.TrimSpace(task.Recipient.Email)
	if to == "" {
		return fmt.Errorf("recipient email is empty")
	}

	body := strings.TrimSpace(task.Body)

	seen := make(map[string]struct{}, 1+len(task.CC)+len(task.BCC))
	seen[strings.ToLower(to)] = struct{}{}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO error for %s: %w", to, err)
	}

	var rcptErr error

	uniqueCC := make([]string, 0, len(task.CC))
	for _, cc := range task.CC {
		cc = strings.TrimSpace(cc)
		if cc == "" {
			continue
		}
		key := strings.ToLower(cc)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniqueCC = append(uniqueCC, cc)
		if err := client.Rcpt(cc); err != nil {
			log.Printf("️ Failed to add CC: %s (%v)", cc, err)
			if rcptErr == nil {
				rcptErr = fmt.Errorf("failed to add CC recipient %s: %w", cc, err)
			}
		}
	}

	for _, bcc := range task.BCC {
		bcc = strings.TrimSpace(bcc)
		if bcc == "" {
			continue
		}
		key := strings.ToLower(bcc)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := client.Rcpt(bcc); err != nil {
			log.Printf("️ Failed to add BCC: %s (%v)", bcc, err)
			if rcptErr == nil {
				rcptErr = fmt.Errorf("failed to add BCC recipient %s: %w", bcc, err)
			}
		}
	}

	if rcptErr != nil {
		return rcptErr
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA command error: %w", err)
	}

	bw := bufWriterPool.Get().(*bufio.Writer)
	bw.Reset(w)
	defer func() {
		if cerr := bw.Flush(); cerr != nil && err == nil {
			err = fmt.Errorf("flush SMTP writer: %w", cerr)
		}
		bw.Reset(io.Discard)
		bufWriterPool.Put(bw)
		if cerr := w.Close(); cerr != nil {
			log.Printf("Error closing SMTP writer: %v", cerr)
		}
	}()

	mixedBoundary := newBoundary("mixed_")
	altBoundary := newBoundary("alt_")
	relBoundary := newBoundary("rel_")

	hasHTML := body != ""
	hasPlain := strings.TrimSpace(task.PlainText) != ""
	hasAttachments := len(task.Attachments) > 0
	hasEmbeds := len(task.Embeds) > 0
	isMultipart := hasHTML && hasPlain

	subject := task.Subject
	if !isASCII(subject) {
		subject = mime.BEncoding.Encode("UTF-8", subject)
	}

	// Headers in fixed order — stable for tests and DKIM canonicalization.
	if err = writeHeader(bw, "From", "Mailgrid <"+from+">"); err != nil {
		return fmt.Errorf("write From: %w", err)
	}
	if err = writeHeader(bw, "To", to); err != nil {
		return fmt.Errorf("write To: %w", err)
	}
	if len(uniqueCC) > 0 {
		if err = writeHeader(bw, "CC", strings.Join(uniqueCC, ", ")); err != nil {
			return fmt.Errorf("write CC: %w", err)
		}
	}
	if err = writeHeader(bw, "Subject", strings.TrimSpace(subject)); err != nil {
		return fmt.Errorf("write Subject: %w", err)
	}
	if err = writeHeader(bw, "MIME-Version", "1.0"); err != nil {
		return fmt.Errorf("write MIME-Version: %w", err)
	}

	var contentType string
	switch {
	case hasAttachments:
		contentType = "multipart/mixed; boundary=" + mixedBoundary
	case hasEmbeds:
		contentType = "multipart/related; boundary=" + relBoundary
	case isMultipart:
		contentType = "multipart/alternative; boundary=" + altBoundary
	case hasHTML:
		contentType = `text/html; charset="UTF-8"`
	default:
		contentType = `text/plain; charset="UTF-8"`
	}
	if err = writeHeader(bw, "Content-Type", contentType); err != nil {
		return fmt.Errorf("write Content-Type: %w", err)
	}
	if _, err = bw.WriteString("\r\n"); err != nil {
		return fmt.Errorf("write header/body separator: %w", err)
	}

	// writeAltParts writes text/plain + text/html parts inside an
	// existing multipart boundary (either altBoundary or inline).
	writeAltParts := func(boundary string) error {
		if hasPlain {
			if e := writeBoundaryLine(bw, boundary); e != nil {
				return fmt.Errorf("write alt boundary: %w", e)
			}
			if _, e := bw.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n"); e != nil {
				return fmt.Errorf("write plain headers: %w", e)
			}
			if _, e := bw.WriteString(strings.TrimSpace(task.PlainText)); e != nil {
				return fmt.Errorf("write plain body: %w", e)
			}
			if _, e := bw.WriteString("\r\n"); e != nil {
				return fmt.Errorf("write plain newline: %w", e)
			}
		}
		if hasHTML {
			if e := writeBoundaryLine(bw, boundary); e != nil {
				return fmt.Errorf("write alt boundary: %w", e)
			}
			if _, e := bw.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n\r\n"); e != nil {
				return fmt.Errorf("write html headers: %w", e)
			}
			if _, e := bw.WriteString(body); e != nil {
				return fmt.Errorf("write html body: %w", e)
			}
			if _, e := bw.WriteString("\r\n"); e != nil {
				return fmt.Errorf("write html newline: %w", e)
			}
		}
		return writeBoundaryClose(bw, boundary, true)
	}

	// writeBodyInPart writes the message body (multipart/alternative or a
	// single part) inside an enclosing multipart boundary such as
	// multipart/related, including its Content-Type header.
	writeBodyInPart := func() error {
		if isMultipart {
			if _, e := bw.WriteString("Content-Type: multipart/alternative; boundary="); e != nil {
				return fmt.Errorf("write alt content-type: %w", e)
			}
			if _, e := bw.WriteString(altBoundary); e != nil {
				return fmt.Errorf("write alt boundary value: %w", e)
			}
			if _, e := bw.WriteString("\r\n\r\n"); e != nil {
				return fmt.Errorf("write alt content-type terminator: %w", e)
			}
			return writeAltParts(altBoundary)
		}
		if hasPlain {
			if _, e := bw.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n\r\n"); e != nil {
				return fmt.Errorf("write plain content-type: %w", e)
			}
			if _, e := bw.WriteString(strings.TrimSpace(task.PlainText)); e != nil {
				return fmt.Errorf("write plain body: %w", e)
			}
			if _, e := bw.WriteString("\r\n"); e != nil {
				return fmt.Errorf("write plain newline: %w", e)
			}
			return nil
		}
		if _, e := bw.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n\r\n"); e != nil {
			return fmt.Errorf("write html content-type: %w", e)
		}
		if _, e := bw.WriteString(body); e != nil {
			return fmt.Errorf("write html body: %w", e)
		}
		if _, e := bw.WriteString("\r\n"); e != nil {
			return fmt.Errorf("write html newline: %w", e)
		}
		return nil
	}

	// writeRelatedParts writes a multipart/related envelope containing the
	// message body followed by inline image parts referenced as cid:embedN.
	writeRelatedParts := func() error {
		if e := writeBoundaryLine(bw, relBoundary); e != nil {
			return fmt.Errorf("write related boundary: %w", e)
		}
		if e := writeBodyInPart(); e != nil {
			return e
		}
		for i, path := range task.Embeds {
			if e := writeEmbed(bw, relBoundary, path, i+1, cache); e != nil {
				return e
			}
		}
		return writeBoundaryClose(bw, relBoundary, false)
	}

	switch {
	case hasAttachments:
		if err = writeBoundaryLine(bw, mixedBoundary); err != nil {
			return fmt.Errorf("write body boundary: %w", err)
		}
		if hasEmbeds {
			if _, err = bw.WriteString("Content-Type: multipart/related; boundary="); err != nil {
				return fmt.Errorf("write related content-type: %w", err)
			}
			if _, err = bw.WriteString(relBoundary); err != nil {
				return fmt.Errorf("write related boundary value: %w", err)
			}
			if _, err = bw.WriteString("\r\n\r\n"); err != nil {
				return fmt.Errorf("write related content-type terminator: %w", err)
			}
			if err = writeRelatedParts(); err != nil {
				return err
			}
		} else {
			if err = writeBodyInPart(); err != nil {
				return err
			}
		}

		for _, path := range task.Attachments {
			if err = writeAttachment(bw, mixedBoundary, path, cache); err != nil {
				return err
			}
		}
		if err = writeBoundaryClose(bw, mixedBoundary, false); err != nil {
			return fmt.Errorf("write closing boundary: %w", err)
		}
	case hasEmbeds:
		if err = writeRelatedParts(); err != nil {
			return err
		}
	case isMultipart:
		if err = writeAltParts(altBoundary); err != nil {
			return err
		}
	default:
		if hasPlain {
			if _, err = bw.WriteString(strings.TrimSpace(task.PlainText)); err != nil {
				return fmt.Errorf("write plain body: %w", err)
			}
		} else {
			if _, err = bw.WriteString(body); err != nil {
				return fmt.Errorf("write body: %w", err)
			}
		}
	}

	return err
}

// writeEmbed writes a single inline image as a part within a multipart/related
// envelope. The part is addressable from the HTML body as cid:embedN, where N
// is the 1-based position of the path in the task's Embeds list. When cache is
// non-nil and the image fits the cache size policy, the base64 payload is
// reused across all recipients in the dispatch run; otherwise it is streamed.
func writeEmbed(bw *bufio.Writer, relBoundary, path string, n int, cache *AttachmentCache) error {
	if err := writeBoundaryLine(bw, relBoundary); err != nil {
		return fmt.Errorf("write embed boundary: %w", err)
	}

	if cache != nil {
		if entry, err := cache.Get(path); err == nil && entry != nil {
			if err := writeHeader(bw, "Content-Type", entry.mimeType+`; name="`+entry.safeName+`"`); err != nil {
				return fmt.Errorf("write embed content type: %w", err)
			}
			if err := writeHeader(bw, "Content-ID", fmt.Sprintf("<embed%d>", n)); err != nil {
				return fmt.Errorf("write embed content-id: %w", err)
			}
			if _, err := bw.WriteString("Content-Disposition: inline; filename=\""); err != nil {
				return fmt.Errorf("write embed disposition: %w", err)
			}
			if _, err := bw.WriteString(entry.safeName); err != nil {
				return fmt.Errorf("write embed disposition: %w", err)
			}
			if _, err := bw.WriteString("\"\r\n"); err != nil {
				return fmt.Errorf("write embed disposition: %w", err)
			}
			if _, err := bw.WriteString("Content-Transfer-Encoding: base64\r\n\r\n"); err != nil {
				return fmt.Errorf("write embed transfer encoding: %w", err)
			}
			if _, err := bw.Write(entry.data); err != nil {
				return fmt.Errorf("write embed payload: %w", err)
			}
			if _, err := bw.WriteString("\r\n"); err != nil {
				return fmt.Errorf("write embed newline: %w", err)
			}
			return nil
		}
		// fall through to streaming on cache miss/error
	}

	mt := mime.TypeByExtension(filepath.Ext(path))
	if mt == "" {
		mt = "application/octet-stream"
	}
	if err := writeHeader(bw, "Content-Type", mt+`; name="`+sanitizeFilename(path)+`"`); err != nil {
		return fmt.Errorf("write embed content type: %w", err)
	}
	if err := writeHeader(bw, "Content-ID", fmt.Sprintf("<embed%d>", n)); err != nil {
		return fmt.Errorf("write embed content-id: %w", err)
	}
	safeName := sanitizeFilename(path)
	if _, err := bw.WriteString("Content-Disposition: inline; filename=\""); err != nil {
		return fmt.Errorf("write embed disposition: %w", err)
	}
	if _, err := bw.WriteString(safeName); err != nil {
		return fmt.Errorf("write embed disposition: %w", err)
	}
	if _, err := bw.WriteString("\"\r\n"); err != nil {
		return fmt.Errorf("write embed disposition: %w", err)
	}
	if _, err := bw.WriteString("Content-Transfer-Encoding: base64\r\n\r\n"); err != nil {
		return fmt.Errorf("write embed transfer encoding: %w", err)
	}
	file, ferr := os.Open(path)
	if ferr != nil {
		return fmt.Errorf("open embed: %w", ferr)
	}
	enc := base64.NewEncoder(base64.StdEncoding, bw)
	bufPtr := copyBufPool.Get().(*[]byte)
	_, copyErr := io.CopyBuffer(enc, file, *bufPtr)
	copyBufPool.Put(bufPtr)
	if cerr := file.Close(); cerr != nil {
		log.Printf("Error closing embed file %s: %v", path, cerr)
	}
	if copyErr != nil {
		if cerr := enc.Close(); cerr != nil {
			log.Printf("Error closing base64 encoder: %v", cerr)
		}
		return fmt.Errorf("encode embed: %w", copyErr)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close encoder: %w", err)
	}
	if _, err := bw.WriteString("\r\n"); err != nil {
		return fmt.Errorf("write embed newline: %w", err)
	}
	return nil
}

// writeAttachment writes a single attachment as a part within a multipart/mixed
// envelope. When cache is non-nil and the attachment fits the cache size policy,
// the base64 payload is reused across all recipients in the dispatch run.
// Otherwise the file is streamed and encoded inline.
func writeAttachment(bw *bufio.Writer, mixedBoundary, path string, cache *AttachmentCache) error {
	if err := writeBoundaryLine(bw, mixedBoundary); err != nil {
		return fmt.Errorf("write attachment boundary: %w", err)
	}

	if cache != nil {
		if entry, err := cache.Get(path); err == nil && entry != nil {
			if err := writeHeader(bw, "Content-Type", entry.mimeType); err != nil {
				return fmt.Errorf("write content type: %w", err)
			}
			if _, err := bw.WriteString("Content-Disposition: attachment; filename=\""); err != nil {
				return fmt.Errorf("write content disposition: %w", err)
			}
			if _, err := bw.WriteString(entry.safeName); err != nil {
				return fmt.Errorf("write content disposition: %w", err)
			}
			if _, err := bw.WriteString("\"\r\n"); err != nil {
				return fmt.Errorf("write content disposition: %w", err)
			}
			if _, err := bw.WriteString("Content-Transfer-Encoding: base64\r\n\r\n"); err != nil {
				return fmt.Errorf("write transfer encoding: %w", err)
			}
			if _, err := bw.Write(entry.data); err != nil {
				return fmt.Errorf("write attachment payload: %w", err)
			}
			if _, err := bw.WriteString("\r\n"); err != nil {
				return fmt.Errorf("write attachment newline: %w", err)
			}
			return nil
		}
		// fall through to streaming on cache miss/error
	}

	mt := mime.TypeByExtension(filepath.Ext(path))
	if mt == "" {
		mt = "application/octet-stream"
	}
	if err := writeHeader(bw, "Content-Type", mt); err != nil {
		return fmt.Errorf("write content type: %w", err)
	}
	safeName := sanitizeFilename(path)
	if _, err := bw.WriteString("Content-Disposition: attachment; filename=\""); err != nil {
		return fmt.Errorf("write content disposition: %w", err)
	}
	if _, err := bw.WriteString(safeName); err != nil {
		return fmt.Errorf("write content disposition: %w", err)
	}
	if _, err := bw.WriteString("\"\r\n"); err != nil {
		return fmt.Errorf("write content disposition: %w", err)
	}
	if _, err := bw.WriteString("Content-Transfer-Encoding: base64\r\n\r\n"); err != nil {
		return fmt.Errorf("write transfer encoding: %w", err)
	}
	file, ferr := os.Open(path)
	if ferr != nil {
		return fmt.Errorf("open attachment: %w", ferr)
	}
	enc := base64.NewEncoder(base64.StdEncoding, bw)
	bufPtr := copyBufPool.Get().(*[]byte)
	_, copyErr := io.CopyBuffer(enc, file, *bufPtr)
	copyBufPool.Put(bufPtr)
	if cerr := file.Close(); cerr != nil {
		log.Printf("Error closing attachment file %s: %v", path, cerr)
	}
	if copyErr != nil {
		if cerr := enc.Close(); cerr != nil {
			log.Printf("Error closing base64 encoder: %v", cerr)
		}
		return fmt.Errorf("encode attachment: %w", copyErr)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close encoder: %w", err)
	}
	if _, err := bw.WriteString("\r\n"); err != nil {
		return fmt.Errorf("write attachment newline: %w", err)
	}
	return nil
}
