package notification

import (
	"context"
	"errors"
	"testing"

	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
)

type stubTemplateRepository struct {
	template Template
	err      error
}

func (s stubTemplateRepository) Find(_ context.Context, _, _ string) (Template, error) {
	return s.template, s.err
}

type recordingMailer struct {
	message Message
	calls   int
}

func (m *recordingMailer) Send(_ context.Context, message Message) (string, error) {
	m.calls++
	m.message = message
	return "mail-local-1", nil
}

func TestSendTemplatedEmailRendersDatabaseTemplate(t *testing.T) {
	mailer := &recordingMailer{}
	service := NewService(stubTemplateRepository{template: Template{
		SubjectTemplate: "Welcome, {{.DisplayName}}",
		HTMLTemplate:    "<h1>Hello {{.DisplayName}}</h1><p>Your workspace is ready.</p>",
	}}, mailer, nil)

	response, err := service.SendTemplatedEmail(context.Background(), &notificationv1.SendTemplatedEmailRequest{
		TemplateKey: "workspace_welcome",
		Locale:      "en",
		Recipient:   "reader@example.test",
		Variables:   map[string]string{"DisplayName": "River"},
	})

	if err != nil {
		t.Fatal(err)
	}
	if response.GetMessageId() != "mail-local-1" {
		t.Fatalf("unexpected message id: %q", response.GetMessageId())
	}
	if mailer.calls != 1 {
		t.Fatalf("expected one send, got %d", mailer.calls)
	}
	if mailer.message.Subject != "Welcome, River" {
		t.Fatalf("unexpected subject: %q", mailer.message.Subject)
	}
	if mailer.message.HTML != "<h1>Hello River</h1><p>Your workspace is ready.</p>" {
		t.Fatalf("unexpected body: %q", mailer.message.HTML)
	}
}

func TestSendTemplatedEmailRejectsMissingVariables(t *testing.T) {
	mailer := &recordingMailer{}
	service := NewService(stubTemplateRepository{template: Template{
		SubjectTemplate: "Welcome, {{.DisplayName}}",
		HTMLTemplate:    "<p>Use {{.ActionURL}}</p>",
	}}, mailer, nil)

	_, err := service.SendTemplatedEmail(context.Background(), &notificationv1.SendTemplatedEmailRequest{
		TemplateKey: "workspace_welcome",
		Locale:      "en",
		Recipient:   "reader@example.test",
		Variables:   map[string]string{"DisplayName": "River"},
	})

	if err == nil || !errors.Is(err, ErrTemplateData) {
		t.Fatalf("expected ErrTemplateData, got %v", err)
	}
	if mailer.calls != 0 {
		t.Fatalf("invalid template must not be sent")
	}
}
