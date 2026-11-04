package email

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/wneessen/go-mail"
)

// SMTPSettings configures SMTPSender.
type SMTPSettings struct {
	Host     string
	Port     int
	Username string
	Password string
	TLS      string // mandatory | opportunistic | none
	From     string
}

// SMTPSender sends through an SMTP server. It dials per message, so a mail
// server restart never leaves a broken connection behind.
type SMTPSender struct {
	from   string
	client *mail.Client
}

// NewSMTPSender validates settings; it does not connect.
func NewSMTPSender(s SMTPSettings) (*SMTPSender, error) {
	policy := map[string]mail.TLSPolicy{
		"mandatory":     mail.TLSMandatory,
		"opportunistic": mail.TLSOpportunistic,
		"none":          mail.NoTLS,
	}[s.TLS]
	opts := []mail.Option{
		mail.WithTLSPolicy(policy),
		mail.WithTLSConfig(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}),
		mail.WithPort(s.Port), // after WithTLSPolicy, which resets the port
	}
	if s.Username != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthPlain), mail.WithUsername(s.Username), mail.WithPassword(s.Password))
	}
	c, err := mail.NewClient(s.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("smtp client: %w", err)
	}
	return &SMTPSender{from: s.From, client: c}, nil
}

// Send delivers m.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	msg := mail.NewMsg()
	if err := msg.From(s.from); err != nil {
		return fmt.Errorf("from address: %w", err)
	}
	if err := msg.To(m.To...); err != nil {
		return fmt.Errorf("to address: %w", err)
	}
	msg.Subject(m.Subject)
	msg.SetBodyString(mail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(mail.TypeTextHTML, m.HTML)
	}
	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}
