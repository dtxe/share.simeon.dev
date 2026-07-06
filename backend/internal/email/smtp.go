package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	gomail "github.com/wneessen/go-mail"
)

// SMTPProvider sends OTP emails via a real SMTP relay. TLS is mandatory:
// TLSMode=starttls uses STARTTLS, and TLSMode=smtps uses implicit TLS.
type SMTPProvider struct {
	Host    string
	Port    int
	User    string
	Pass    string
	From    string
	TLSMode string
	Timeout time.Duration
}

func (p *SMTPProvider) SendOTP(ctx context.Context, to, code string) error {
	subject := "Your Share sign-in code"
	body := fmt.Sprintf("Your sign-in code is: %s\r\n\r\nThis code expires in a few minutes. If you didn't request this, you can ignore this email.\r\n", code)

	msg := gomail.NewMsg()
	if err := msg.From(p.From); err != nil {
		return fmt.Errorf("email: from address: %w", err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("email: recipient address: %w", err)
	}
	msg.Subject(subject)
	msg.SetDateWithValue(time.Now())
	msg.SetBodyString(gomail.TypeTextPlain, body)

	timeout := p.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	opts := []gomail.Option{
		gomail.WithPort(p.Port),
		gomail.WithTimeout(timeout),
		gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
		gomail.WithUsername(p.User),
		gomail.WithPassword(p.Pass),
		gomail.WithTLSConfig(&tls.Config{ServerName: p.Host, MinVersion: tls.VersionTLS12}),
	}
	switch p.TLSMode {
	case "starttls":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case "smtps":
		opts = append(opts, gomail.WithSSL())
	default:
		return fmt.Errorf("email: unsupported SMTP TLS mode %q", p.TLSMode)
	}

	client, err := gomail.NewClient(p.Host, opts...)
	if err != nil {
		return fmt.Errorf("email: smtp client: %w", err)
	}

	sendCtx := ctx
	if _, ok := sendCtx.Deadline(); !ok {
		var cancel context.CancelFunc
		sendCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := client.DialAndSendWithContext(sendCtx, msg); err != nil {
		return fmt.Errorf("email: send OTP: %w", err)
	}
	return nil
}
