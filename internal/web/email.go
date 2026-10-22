// SPDX-License-Identifier: MIT

package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ab0oo/gopds/internal/kindle"
	"github.com/ab0oo/gopds/internal/mailer"
	"github.com/go-chi/chi/v5"
)

// Emailing a book is public, like downloading it. That makes it reachable by
// anyone who can reach the server, so it is fenced in three ways: recipients
// must be Kindle addresses unless EMAIL_ALLOWED_DOMAINS says otherwise, each
// client gets a limited number of sends, and only a couple of sends run at
// once, since each holds a whole book in memory.

const (
	emailSendsPerHour  = 20
	emailMaxConcurrent = 2
)

var defaultEmailDomains = []string{"kindle.com", "free.kindle.com", "kindle.cn"}

type emailState struct {
	cfg     mailer.Config
	enabled bool
	// domains is nil when any domain is allowed.
	domains []string

	mu     sync.Mutex
	recent map[string][]time.Time
	slots  chan struct{}

	// send is mailer.Send, replaceable in tests.
	send func(mailer.Config, string, []byte) error
}

func newEmailState() *emailState {
	st := &emailState{
		recent: map[string][]time.Time{},
		slots:  make(chan struct{}, emailMaxConcurrent),
		send:   mailer.Send,
	}
	cfg, ok, err := mailer.ConfigFromEnv()
	if err != nil {
		log.Printf("warning: Email Book is disabled: %v", err)
	} else if ok {
		st.cfg, st.enabled = cfg, true
		log.Printf("emailing books enabled via %s:%d as %s", cfg.Server, cfg.Port, cfg.From)
	}
	st.domains = parseEmailDomains(os.Getenv("EMAIL_ALLOWED_DOMAINS"))
	return st
}

// parseEmailDomains reads a comma-separated domain list; "*" allows any
// domain and an empty value means the Kindle defaults.
func parseEmailDomains(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultEmailDomains
	}
	var out []string
	for _, d := range strings.Split(raw, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "*" {
			return nil
		}
		if d != "" {
			out = append(out, strings.TrimPrefix(d, "@"))
		}
	}
	if len(out) == 0 {
		return defaultEmailDomains
	}
	return out
}

func (st *emailState) domainAllowed(address string) bool {
	if st.domains == nil {
		return true
	}
	domain := strings.ToLower(address[strings.LastIndex(address, "@")+1:])
	for _, d := range st.domains {
		if domain == d {
			return true
		}
	}
	return false
}

// allow records a send for client and reports whether it is within the
// hourly limit.
func (st *emailState) allow(client string, now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	cutoff := now.Add(-time.Hour)
	kept := st.recent[client][:0]
	for _, t := range st.recent[client] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= emailSendsPerHour {
		st.recent[client] = kept
		return false
	}
	st.recent[client] = append(kept, now)
	return true
}

func (s *Server) emailState() *emailState {
	s.emailOnce.Do(func() { s.email = newEmailState() })
	return s.email
}

// HandleEmailStatus tells the UI whether to offer emailing, and to whom.
func (s *Server) HandleEmailStatus(w http.ResponseWriter, r *http.Request) {
	st := s.emailState()
	w.Header().Set("Content-Type", "application/json")
	// The sender is shown so readers know which address to add to Amazon's
	// approved sender list; without that, Kindle silently drops the mail.
	from := ""
	if st.enabled {
		from = st.cfg.From
	}
	_ = json.NewEncoder(w).Encode(struct {
		Enabled bool     `json:"enabled"`
		From    string   `json:"from,omitempty"`
		Domains []string `json:"domains"`
	}{Enabled: st.enabled, From: from, Domains: st.domains})
}

type emailBookRequest struct {
	To string `json:"to"`
}

type emailBookResponse struct {
	OK       bool     `json:"ok"`
	To       string   `json:"to"`
	Filename string   `json:"filename"`
	Fixes    []string `json:"fixes"`
	Warnings []string `json:"warnings"`
}

// HandleEmailBook sends a book, repaired for Send to Kindle, to one address.
func (s *Server) HandleEmailBook(w http.ResponseWriter, r *http.Request) {
	st := s.emailState()
	if !st.enabled {
		http.Error(w, "Emailing books is not configured on this server", http.StatusServiceUnavailable)
		return
	}

	var req emailBookRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(req.To))
	if err != nil || !strings.Contains(addr.Address, "@") {
		http.Error(w, "Enter a valid email address", http.StatusBadRequest)
		return
	}
	to := addr.Address
	if !st.domainAllowed(to) {
		http.Error(w, "Books can only be sent to "+strings.Join(st.domains, ", ")+" addresses", http.StatusBadRequest)
		return
	}

	book, err := s.db.GetBookByID(chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Book not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	client := r.RemoteAddr
	if host, _, err := net.SplitHostPort(client); err == nil {
		client = host
	}
	if !st.allow(client, time.Now()) {
		http.Error(w, fmt.Sprintf("Too many books emailed; the limit is %d an hour", emailSendsPerHour), http.StatusTooManyRequests)
		return
	}

	select {
	case st.slots <- struct{}{}:
		defer func() { <-st.slots }()
	case <-r.Context().Done():
		return
	}

	bookPath, err := s.resolveBookPath(book)
	if err != nil {
		http.Error(w, "Book file not found", http.StatusNotFound)
		return
	}
	prepared, err := kindle.PrepareEPUB(bookPath)
	if err != nil {
		log.Printf("[email] prepare failed book_id=%d err=%v", book.ID, err)
		http.Error(w, "The EPUB could not be read", http.StatusUnprocessableEntity)
		return
	}
	if len(prepared.Data) > kindle.MaxEmailBytes {
		http.Error(w, fmt.Sprintf("This book is %d MB; Send to Kindle accepts at most %d MB by email",
			len(prepared.Data)>>20, kindle.MaxEmailBytes>>20), http.StatusRequestEntityTooLarge)
		return
	}

	filename := kindle.AttachmentName(book.Title, book.Author)
	subject := strings.TrimSpace(book.Title)
	if subject == "" {
		subject = "Book from GoPDS"
	}
	body := fmt.Sprintf("%s\nby %s\n\nSent from GoPDS.", subject, strings.TrimSpace(book.Author))
	msg, err := mailer.BuildMessage(st.cfg.From, to, subject, body, mailer.Attachment{
		Filename:    filename,
		ContentType: "application/epub+zip",
		Data:        prepared.Data,
	}, time.Now())
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := st.send(st.cfg, to, msg); err != nil {
		log.Printf("[email] send failed book_id=%d to=%s err=%v", book.ID, to, err)
		http.Error(w, "Sending failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	log.Printf("[email] sent book_id=%d to=%s file=%s fixes=%d", book.ID, to, filename, len(prepared.Fixes))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(emailBookResponse{
		OK:       true,
		To:       to,
		Filename: filename,
		Fixes:    nonNil(prepared.Fixes),
		Warnings: nonNil(prepared.Warnings),
	})
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
