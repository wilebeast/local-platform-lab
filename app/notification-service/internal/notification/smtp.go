package notification

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"net/smtp"
	"strings"

	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
)

type SMTPMailer struct {
	address string
	from    string
}

func NewSMTPMailer(address, from string) *SMTPMailer {
	return &SMTPMailer{address: address, from: from}
}

func (m *SMTPMailer) Send(ctx context.Context, message Message) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	messageID, err := randomID()
	if err != nil {
		return "", err
	}
	raw, err := buildMIME(m.from, message, messageID)
	if err != nil {
		return "", err
	}
	if err := smtp.SendMail(m.address, nil, m.from, []string{message.Recipient}, raw); err != nil {
		return "", err
	}
	return messageID, nil
}

func buildMIME(from string, message Message, messageID string) ([]byte, error) {
	boundary := "mixed-" + messageID
	var output bytes.Buffer
	fmt.Fprintf(&output, "From: %s\r\nTo: %s\r\n", from, message.Recipient)
	fmt.Fprintf(&output, "Subject: %s\r\n", mime.QEncoding.Encode("UTF-8", message.Subject))
	fmt.Fprintf(&output, "Message-ID: <%s@local.test>\r\nMIME-Version: 1.0\r\n", messageID)
	if len(message.Attachments) == 0 {
		output.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		output.WriteString(message.HTML)
		return output.Bytes(), nil
	}
	fmt.Fprintf(&output, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&output, "--%s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s\r\n", boundary, message.HTML)
	for _, attachment := range message.Attachments {
		if strings.ContainsAny(attachment.Filename, "\r\n") {
			return nil, fmt.Errorf("invalid attachment filename")
		}
		contentType := attachment.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		fmt.Fprintf(&output, "--%s\r\nContent-Type: %s\r\n", boundary, contentType)
		fmt.Fprintf(&output, "Content-Disposition: attachment; filename=%q\r\n", attachment.Filename)
		output.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
		encoded := base64.StdEncoding.EncodeToString(attachment.Content)
		for len(encoded) > 76 {
			output.WriteString(encoded[:76] + "\r\n")
			encoded = encoded[76:]
		}
		output.WriteString(encoded + "\r\n")
	}
	fmt.Fprintf(&output, "--%s--\r\n", boundary)
	return output.Bytes(), nil
}

func randomID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

type TemplateCodeSender struct {
	templates TemplateRepository
	mailer    Mailer
}

func NewTemplateCodeSender(templates TemplateRepository, mailer Mailer) *TemplateCodeSender {
	return &TemplateCodeSender{templates: templates, mailer: mailer}
}

func (s *TemplateCodeSender) SendCode(ctx context.Context, recipient, purpose, locale, code string) error {
	service := NewService(s.templates, s.mailer, nil)
	_, err := service.SendTemplatedEmail(ctx, &notificationv1.SendTemplatedEmailRequest{
		TemplateKey: "email_verification", Locale: locale, Recipient: recipient,
		Variables: map[string]string{"Code": code, "Purpose": purpose},
	})
	return err
}
