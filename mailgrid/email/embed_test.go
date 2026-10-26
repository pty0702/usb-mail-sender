package email

import (
	"bufio"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bravo1goingdark/mailgrid/config"
	"github.com/bravo1goingdark/mailgrid/parser"
)

// fakeSMTPServer speaks just enough SMTP over a net.Pipe for smtp.Client to
// complete a session, and captures the DATA payload for inspection.
type fakeSMTPServer struct {
	conn net.Conn
	data string
}

func newFakeSMTPServer(t *testing.T) (*smtp.Client, *fakeSMTPServer) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	srv := &fakeSMTPServer{conn: serverConn}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()
		r := bufio.NewReader(serverConn)
		resp := func(line string) {
			serverConn.Write([]byte(line + "\r\n"))
		}
		resp("220 test.local ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				resp("250-test.local")
				resp("250 AUTH PLAIN")
			case strings.HasPrefix(cmd, "AUTH"):
				resp("235 ok")
			case strings.HasPrefix(cmd, "MAIL FROM"):
				resp("250 ok")
			case strings.HasPrefix(cmd, "RCPT TO"):
				resp("250 ok")
			case strings.HasPrefix(cmd, "DATA"):
				resp("354 go ahead")
				var body strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if strings.TrimSpace(l) == "." {
						break
					}
					body.WriteString(l)
				}
				srv.data = body.String()
				resp("250 accepted")
			case strings.HasPrefix(cmd, "QUIT"):
				resp("221 bye")
				return
			}
		}
	}()

	client, err := smtp.NewClient(clientConn, "test.local")
	if err != nil {
		t.Fatalf("smtp.NewClient: %v", err)
	}
	t.Cleanup(func() {
		clientConn.Close()
		<-done
	})
	return client, srv
}

// TestSendWithClientEmbedMIME verifies that an embedded image produces a
// multipart/related message whose image part is addressable as cid:embed1.
func TestSendWithClientEmbedMIME(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "poster.png")
	if err := os.WriteFile(img, []byte("fakepngbytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	client, srv := newFakeSMTPServer(t)

	cfg := config.SMTPConfig{From: "sender@example.com"}
	task := Task{
		Recipient: parser.Recipient{Email: "to@example.com", Data: map[string]string{"name": "To Example"}},
		Subject:   "Hi",
		Body:      `<html><body><p>Hi {{.name}},</p><img src="cid:embed1"></body></html>`,
		Embeds:    []string{img},
	}

	if err := SendWithClient(client, cfg, task, nil); err != nil {
		t.Fatalf("SendWithClient: %v", err)
	}

	for _, want := range []string{
		"Content-Type: multipart/related; boundary=rel_",
		"Content-Type: text/html; charset=\"UTF-8\"",
		"Content-Type: image/png; name=\"poster.png\"",
		"Content-ID: <embed1>",
		"Content-Disposition: inline; filename=\"poster.png\"",
		"Content-Transfer-Encoding: base64",
		`<img src="cid:embed1">`,
	} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("message missing %q\nGot DATA:\n%s", want, srv.data)
		}
	}
}

// TestSendWithClientEmbedAndAttachment verifies the nested
// multipart/mixed > multipart/related structure when both are present.
func TestSendWithClientEmbedAndAttachment(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "poster.png")
	att := filepath.Join(dir, "price.pdf")
	if err := os.WriteFile(img, []byte("fakepngbytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(att, []byte("fakepdfbytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	client, srv := newFakeSMTPServer(t)

	cfg := config.SMTPConfig{From: "sender@example.com"}
	task := Task{
		Recipient:   parser.Recipient{Email: "to@example.com", Data: map[string]string{"name": "To Example"}},
		Subject:     "Hi",
		Body:        `<html><body><img src="cid:embed1"></body></html>`,
		Embeds:      []string{img},
		Attachments: []string{att},
	}

	if err := SendWithClient(client, cfg, task, nil); err != nil {
		t.Fatalf("SendWithClient: %v", err)
	}

	for _, want := range []string{
		"Content-Type: multipart/mixed; boundary=mixed_",
		"Content-Type: multipart/related; boundary=rel_",
		"Content-ID: <embed1>",
		"Content-Disposition: attachment; filename=\"price.pdf\"",
	} {
		if !strings.Contains(srv.data, want) {
			t.Errorf("message missing %q\nGot DATA:\n%s", want, srv.data)
		}
	}
}
