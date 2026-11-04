// Package email sends mail through a Sender. Adapters: smtp (any SMTP
// server, Mailpit locally) and log (writes the message to the log; used in
// tests and when no provider is configured).
package email

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	texttemplate "text/template"
)

// Message is one outbound email.
type Message struct {
	To      []string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	htmlTmpl = htmltemplate.Must(htmltemplate.ParseFS(templateFS, "templates/*.html.tmpl"))
	textTmpl = texttemplate.Must(texttemplate.ParseFS(templateFS, "templates/*.txt.tmpl"))
)

// Render builds a Message from the named template pair
// templates/<name>.html.tmpl and templates/<name>.txt.tmpl. The text
// template must define a "subject" block.
func Render(name string, to []string, data any) (Message, error) {
	var subj, txt, html bytes.Buffer
	if err := textTmpl.ExecuteTemplate(&subj, name+".subject", data); err != nil {
		return Message{}, fmt.Errorf("render %s subject: %w", name, err)
	}
	if err := textTmpl.ExecuteTemplate(&txt, name+".txt.tmpl", data); err != nil {
		return Message{}, fmt.Errorf("render %s text: %w", name, err)
	}
	if err := htmlTmpl.ExecuteTemplate(&html, name+".html.tmpl", data); err != nil {
		return Message{}, fmt.Errorf("render %s html: %w", name, err)
	}
	return Message{To: to, Subject: subj.String(), Text: txt.String(), HTML: html.String()}, nil
}

// LogSender writes messages to the log instead of sending them.
type LogSender struct{ Log *slog.Logger }

// Send logs recipients and subject. Bodies are not logged: they can hold
// personal data.
func (s LogSender) Send(ctx context.Context, m Message) error {
	s.Log.InfoContext(ctx, "email (log driver, not sent)", "to_count", len(m.To), "subject", m.Subject)
	return nil
}
