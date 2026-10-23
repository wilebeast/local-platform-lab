package notification

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/mail"
	"strings"

	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var ErrTemplateData = errors.New("invalid template data")

type Template struct {
	SubjectTemplate string
	HTMLTemplate    string
}

type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
}

type Message struct {
	Recipient   string
	Subject     string
	HTML        string
	Attachments []Attachment
}

type TemplateRepository interface {
	Find(ctx context.Context, key, locale string) (Template, error)
}

type Mailer interface {
	Send(ctx context.Context, message Message) (string, error)
}

type CodeManager interface {
	Issue(ctx context.Context, recipient, purpose, locale string) (bool, int64, error)
	Verify(ctx context.Context, recipient, purpose, code string) (bool, error)
}

type Service struct {
	notificationv1.UnimplementedNotificationServiceServer
	templates TemplateRepository
	mailer    Mailer
	codes     CodeManager
}

func NewService(templates TemplateRepository, mailer Mailer, codes CodeManager) *Service {
	return &Service{templates: templates, mailer: mailer, codes: codes}
}

func (s *Service) SendTemplatedEmail(ctx context.Context, request *notificationv1.SendTemplatedEmailRequest) (*notificationv1.SendTemplatedEmailResponse, error) {
	if request.GetTemplateKey() == "" || request.GetRecipient() == "" {
		return nil, status.Error(codes.InvalidArgument, "template_key and recipient are required")
	}
	if _, err := mail.ParseAddress(request.GetRecipient()); err != nil {
		return nil, status.Error(codes.InvalidArgument, "recipient is invalid")
	}
	locale := strings.TrimSpace(request.GetLocale())
	if locale == "" {
		locale = "en"
	}
	tpl, err := s.templates.Find(ctx, request.GetTemplateKey(), locale)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "template unavailable: %v", err)
	}
	subject, err := render("subject", tpl.SubjectTemplate, request.GetVariables())
	if err != nil {
		return nil, err
	}
	htmlBody, err := render("html", tpl.HTMLTemplate, request.GetVariables())
	if err != nil {
		return nil, err
	}
	attachments := make([]Attachment, 0, len(request.GetAttachments()))
	for _, item := range request.GetAttachments() {
		attachments = append(attachments, Attachment{Filename: item.GetFilename(), ContentType: item.GetContentType(), Content: item.GetContent()})
	}
	messageID, err := s.mailer.Send(ctx, Message{
		Recipient: request.GetRecipient(), Subject: subject, HTML: htmlBody, Attachments: attachments,
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "email delivery failed: %v", err)
	}
	return &notificationv1.SendTemplatedEmailResponse{MessageId: messageID}, nil
}

func render(name, source string, variables map[string]string) (string, error) {
	tpl, err := template.New(name).Option("missingkey=error").Parse(source)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrTemplateData, err)
	}
	var output bytes.Buffer
	if err := tpl.Execute(&output, variables); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTemplateData, err)
	}
	return output.String(), nil
}

func (s *Service) IssueEmailCode(ctx context.Context, request *notificationv1.IssueEmailCodeRequest) (*notificationv1.IssueEmailCodeResponse, error) {
	if s.codes == nil {
		return nil, status.Error(codes.Unimplemented, "email codes are not configured")
	}
	sent, expiresIn, err := s.codes.Issue(ctx, request.GetRecipient(), request.GetPurpose(), request.GetLocale())
	if err != nil {
		return nil, err
	}
	return &notificationv1.IssueEmailCodeResponse{Sent: sent, ExpiresInSeconds: expiresIn}, nil
}

func (s *Service) VerifyEmailCode(ctx context.Context, request *notificationv1.VerifyEmailCodeRequest) (*notificationv1.VerifyEmailCodeResponse, error) {
	if s.codes == nil {
		return nil, status.Error(codes.Unimplemented, "email codes are not configured")
	}
	valid, err := s.codes.Verify(ctx, request.GetRecipient(), request.GetPurpose(), request.GetCode())
	if err != nil {
		return nil, err
	}
	return &notificationv1.VerifyEmailCodeResponse{Valid: valid}, nil
}
