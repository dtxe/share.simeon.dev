package email

import (
	"context"
	"fmt"
	"net"
	"net/smtp"
	"time"
)

// SMTPProvider sends OTP emails via plain SMTP. Works unauthenticated against
// mailpit in dev, and against any real relay (SES/Postmark/Sendgrid SMTP) in
// prod by setting SMTPUser/SMTPPass.
type SMTPProvider struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

func (p *SMTPProvider) SendOTP(ctx context.Context, to, code string) error {
	addr := net.JoinHostPort(p.Host, fmt.Sprintf("%d", p.Port))

	subject := "Your Cher sign-in code"
	body := fmt.Sprintf("Your sign-in code is: %s\r\n\r\nThis code expires in a few minutes. If you didn't request this, you can ignore this email.\r\n", code)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\n\r\n%s",
		p.From, to, subject, time.Now().Format(time.RFC1123Z), body)

	var auth smtp.Auth
	if p.User != "" {
		auth = smtp.PlainAuth("", p.User, p.Pass, p.Host)
	}

	return smtp.SendMail(addr, auth, p.From, []string{to}, []byte(msg))
}
