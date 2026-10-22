// SPDX-License-Identifier: MIT

// Package mailer sends a single file as an email attachment over SMTP.
package mailer

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the SMTP account books are sent from.
type Config struct {
	Server string
	Port   int
	User   string
	Pass   string
	// From is the sender address. It defaults to User, and for Send to
	// Kindle it must be on the recipient's approved sender list.
	From string
}

// ConfigFromEnv reads SMTP_SERVER, SMTP_PORT, SMTP_USER, SMTP_PASS and the
// optional SMTP_FROM. All four SMTP_SERVER, SMTP_PORT, SMTP_USER and SMTP_PASS
// are required; ok is false, and emailing stays off, unless every one is set.
// With none of server, user and password set (a port alone is a common
// compose default) that is not an error; a partial setup is, so the log can
// say what is missing.
func ConfigFromEnv() (cfg Config, ok bool, err error) {
	cfg = Config{
		Server: strings.TrimSpace(os.Getenv("SMTP_SERVER")),
		User:   strings.TrimSpace(os.Getenv("SMTP_USER")),
		Pass:   os.Getenv("SMTP_PASS"),
		From:   strings.TrimSpace(os.Getenv("SMTP_FROM")),
	}
	rawPort := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if cfg.Server == "" && cfg.User == "" && cfg.Pass == "" {
		return cfg, false, nil
	}

	var missing []string
	for _, v := range []struct{ name, value string }{
		{"SMTP_SERVER", cfg.Server},
		{"SMTP_PORT", rawPort},
		{"SMTP_USER", cfg.User},
		{"SMTP_PASS", cfg.Pass},
	} {
		if strings.TrimSpace(v.value) == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return cfg, false, fmt.Errorf("%s not set", strings.Join(missing, ", "))
	}

	p, convErr := strconv.Atoi(rawPort)
	if convErr != nil || p < 1 || p > 65535 {
		return cfg, false, fmt.Errorf("SMTP_PORT %q is not a valid port", rawPort)
	}
	cfg.Port = p
	if cfg.From == "" {
		cfg.From = cfg.User
	}
	if _, parseErr := mail.ParseAddress(cfg.From); parseErr != nil {
		return cfg, false, fmt.Errorf("sender address %q (SMTP_FROM, or SMTP_USER when SMTP_FROM is unset) is not an email address", cfg.From)
	}
	return cfg, true, nil
}

// Attachment is the file being sent.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// BuildMessage renders a multipart/mixed email with a short text body and
// one base64 attachment.
func BuildMessage(from, to, subject, body string, att Attachment, now time.Time) ([]byte, error) {
	// Header values come partly from book metadata; a line break in one
	// would let it add headers of its own.
	from, to, subject = oneLine(from), oneLine(to), oneLine(subject)
	att.Filename = oneLine(att.Filename)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	host := "gopds.local"
	if at := strings.LastIndex(from, "@"); at >= 0 {
		host = from[at+1:]
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)

	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"Date: " + now.Format(time.RFC1123Z),
		"Message-ID: <" + hex.EncodeToString(id) + "@" + host + ">",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=" + mw.Boundary(),
	}
	head := strings.Join(headers, "\r\n") + "\r\n\r\n"

	text, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=utf-8"},
		"Content-Transfer-Encoding": {"8bit"},
	})
	if err != nil {
		return nil, err
	}
	if _, err := text.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n") + "\r\n")); err != nil {
		return nil, err
	}

	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType(att.ContentType, map[string]string{"name": att.Filename})},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename})},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return nil, err
	}
	if err := writeBase64Lines(part, att.Data); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return append([]byte(head), buf.Bytes()...), nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}

// writeBase64Lines writes base64 in 76-character lines, as RFC 2045 requires.
func writeBase64Lines(w interface{ Write([]byte) (int, error) }, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 {
		if _, err := w.Write([]byte(enc[:76] + "\r\n")); err != nil {
			return err
		}
		enc = enc[76:]
	}
	_, err := w.Write([]byte(enc + "\r\n"))
	return err
}

const dialTimeout = 30 * time.Second

// sendDeadline bounds a whole send, including uploading a large attachment.
const sendDeadline = 5 * time.Minute

// Send delivers msg to one recipient. Port 465 uses implicit TLS; any other
// port upgrades with STARTTLS when the server offers it, and refuses to send
// credentials over an unencrypted connection.
func Send(cfg Config, to string, msg []byte) error {
	addr := net.JoinHostPort(cfg.Server, strconv.Itoa(cfg.Port))
	tlsConfig := &tls.Config{ServerName: cfg.Server, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	var err error
	if cfg.Port == 465 {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: dialTimeout}, "tcp", addr, tlsConfig)
	} else {
		conn, err = net.DialTimeout("tcp", addr, dialTimeout)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(sendDeadline))

	c, err := smtp.NewClient(conn, cfg.Server)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SMTP greeting from %s: %w", addr, err)
	}
	defer c.Close()

	if hostname, err := os.Hostname(); err == nil && strings.Contains(hostname, ".") {
		_ = c.Hello(hostname)
	}

	_, encrypted := conn.(*tls.Conn)
	if !encrypted {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
			encrypted = true
		}
	}

	if cfg.User != "" {
		if !encrypted && !isLocalhost(cfg.Server) {
			return errors.New("the SMTP server does not offer TLS; refusing to send the password unencrypted")
		}
		if err := c.Auth(chooseAuth(c, cfg)); err != nil {
			return fmt.Errorf("SMTP login failed: %w", err)
		}
	}

	if err := c.Mail(cfg.From); err != nil {
		return fmt.Errorf("sender %s rejected: %w", cfg.From, err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("recipient %s rejected: %w", to, err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("server refused the message: %w", err)
	}
	return c.Quit()
}

func isLocalhost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// chooseAuth prefers PLAIN and falls back to LOGIN, which some servers
// (Office 365 among them) still require.
func chooseAuth(c *smtp.Client, cfg Config) smtp.Auth {
	if ok, mechs := c.Extension("AUTH"); ok {
		upper := " " + strings.ToUpper(mechs) + " "
		if !strings.Contains(upper, " PLAIN ") && strings.Contains(upper, " LOGIN ") {
			return &loginAuth{user: cfg.User, pass: cfg.Pass}
		}
	}
	return smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Server)
}

type loginAuth struct{ user, pass string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocalhost(server.Name) {
		return "", nil, errors.New("refusing LOGIN auth over an unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", fromServer)
}
