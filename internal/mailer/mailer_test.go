// SPDX-License-Identifier: MIT

package mailer

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestBuildMessage(t *testing.T) {
	data := bytes.Repeat([]byte("epub-bytes-"), 500)
	msg, err := BuildMessage("books@example.com", "reader@kindle.com", "Demons Don’t Dream\r\nBcc: evil@example.com",
		"body", Attachment{Filename: "Demons_Dont_Dream.epub", ContentType: "application/epub+zip", Data: data},
		time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}

	m, err := mail.ReadMessage(bytes.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	if m.Header.Get("Bcc") != "" {
		t.Fatal("a line break in the subject injected a header")
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if subject != "Demons Don’t Dream Bcc: evil@example.com" {
		t.Errorf("subject = %q", subject)
	}
	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/mixed" {
		t.Fatalf("content type %q, %v", mediaType, err)
	}

	mr := multipart.NewReader(m.Body, params["boundary"])
	if _, err := mr.NextPart(); err != nil { // text body
		t.Fatal(err)
	}
	att, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if ct := att.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/epub+zip") {
		t.Errorf("attachment content type %q", ct)
	}
	if att.FileName() != "Demons_Dont_Dream.epub" {
		t.Errorf("filename %q", att.FileName())
	}
	raw, _ := io.ReadAll(att)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("base64 line of %d characters", len(line))
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
	if err != nil || !bytes.Equal(decoded, data) {
		t.Errorf("attachment did not round-trip: %v", err)
	}
}

// fakeSMTP is a minimal SMTP server that records one session.
type fakeSMTP struct {
	authMechs string
	auth      string
	from, to  string
	data      string
}

func (f *fakeSMTP) serve(t *testing.T, ln net.Listener) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	read := func() string {
		line, _ := r.ReadString('\n')
		return strings.TrimRight(line, "\r\n")
	}
	say("220 fake ESMTP")
	for {
		line := read()
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			say("250 AUTH " + f.authMechs)
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			f.auth = "PLAIN " + strings.TrimSpace(line[len("AUTH PLAIN"):])
			say("235 ok")
		case cmd == "AUTH LOGIN":
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := base64.StdEncoding.DecodeString(read())
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := base64.StdEncoding.DecodeString(read())
			f.auth = "LOGIN " + string(u) + ":" + string(p)
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			f.from = line[len("MAIL FROM:"):]
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			f.to = line[len("RCPT TO:"):]
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l := read()
				if l == "." {
					break
				}
				b.WriteString(l + "\n")
			}
			f.data = b.String()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}

func runSend(t *testing.T, mechs string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := &fakeSMTP{authMechs: mechs}
	done := make(chan struct{})
	go func() { srv.serve(t, ln); close(done) }()

	cfg := Config{Server: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, User: "books@example.com", Pass: "s3cret", From: "books@example.com"}
	if err := Send(cfg, "reader@kindle.com", []byte("Subject: hi\r\n\r\nhello\r\n")); err != nil {
		t.Fatal(err)
	}
	<-done
	return srv
}

func TestSendPlainAuth(t *testing.T) {
	srv := runSend(t, "PLAIN LOGIN")
	want := "PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00books@example.com\x00s3cret"))
	if srv.auth != want {
		t.Errorf("auth = %q, want %q", srv.auth, want)
	}
	if srv.from != "<books@example.com>" || srv.to != "<reader@kindle.com>" {
		t.Errorf("envelope from %q to %q", srv.from, srv.to)
	}
	if !strings.Contains(srv.data, "hello") {
		t.Errorf("data = %q", srv.data)
	}
}

func TestHeloName(t *testing.T) {
	for _, tc := range []struct{ hostname, from, want string }{
		{"books.example.net", "gopds@example.com", "books.example.net"},
		{"3f2a9c1b7d4e", "gopds@example.com", "example.com"},
		{"localhost.localdomain", "Books <gopds@example.com>", "example.com"},
		{"", "gopds@example.com", "example.com"},
		{"3f2a9c1b7d4e", "gopds@intranet", "localhost"},
	} {
		if got := heloName(tc.hostname, tc.from); got != tc.want {
			t.Errorf("heloName(%q, %q) = %q, want %q", tc.hostname, tc.from, got, tc.want)
		}
	}
}

func TestSendFallsBackToLoginAuth(t *testing.T) {
	srv := runSend(t, "LOGIN")
	if srv.auth != "LOGIN books@example.com:s3cret" {
		t.Errorf("auth = %q", srv.auth)
	}
}

func TestConfigFromEnv(t *testing.T) {
	set := func(server, port, user, pass, from string) {
		t.Setenv("SMTP_SERVER", server)
		t.Setenv("SMTP_PORT", port)
		t.Setenv("SMTP_USER", user)
		t.Setenv("SMTP_PASS", pass)
		t.Setenv("SMTP_FROM", from)
	}

	// Nothing set, or only the port a compose file defaults: off, quietly.
	for _, port := range []string{"", "587"} {
		set("", port, "", "", "")
		if _, ok, err := ConfigFromEnv(); ok || err != nil {
			t.Errorf("port %q only: ok=%v err=%v, want off without error", port, ok, err)
		}
	}

	// Any of the four missing: off, and the error names what is missing.
	for _, c := range []struct{ server, port, user, pass, missing string }{
		{"smtp.example.com", "587", "books@example.com", "", "SMTP_PASS"},
		{"smtp.example.com", "", "books@example.com", "pw", "SMTP_PORT"},
		{"smtp.example.com", "587", "", "", "SMTP_USER, SMTP_PASS"},
		{"", "587", "books@example.com", "pw", "SMTP_SERVER"},
	} {
		set(c.server, c.port, c.user, c.pass, "")
		_, ok, err := ConfigFromEnv()
		if ok || err == nil || !strings.Contains(err.Error(), c.missing) {
			t.Errorf("missing %s: ok=%v err=%v", c.missing, ok, err)
		}
	}

	set("smtp.example.com", "587", "books@example.com", "pw", "")
	cfg, ok, err := ConfigFromEnv()
	if !ok || err != nil || cfg.Port != 587 || cfg.From != "books@example.com" {
		t.Fatalf("complete: %+v ok=%v err=%v", cfg, ok, err)
	}

	set("smtp.example.com", "587", "AKIAEXAMPLEKEY", "pw", "") // e.g. an SES login
	if _, ok, err := ConfigFromEnv(); ok || err == nil {
		t.Error("a non-address sender should keep emailing off with an error")
	}
	set("smtp.example.com", "587", "AKIAEXAMPLEKEY", "pw", "books@example.com")
	if _, ok, err := ConfigFromEnv(); !ok || err != nil {
		t.Errorf("SMTP_FROM should supply the sender: ok=%v err=%v", ok, err)
	}

	set("smtp.example.com", "smtp", "books@example.com", "pw", "")
	if _, ok, err := ConfigFromEnv(); ok || err == nil {
		t.Error("a bad port should be an error")
	}
}
