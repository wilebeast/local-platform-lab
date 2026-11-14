package registration

import (
	"context"
	"testing"

	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
	"google.golang.org/grpc"
)

type recordingNotificationClient struct {
	request *notificationv1.IssueEmailCodeRequest
}

func (c *recordingNotificationClient) SendTemplatedEmail(context.Context, *notificationv1.SendTemplatedEmailRequest, ...grpc.CallOption) (*notificationv1.SendTemplatedEmailResponse, error) {
	return &notificationv1.SendTemplatedEmailResponse{}, nil
}

func (c *recordingNotificationClient) IssueEmailCode(_ context.Context, request *notificationv1.IssueEmailCodeRequest, _ ...grpc.CallOption) (*notificationv1.IssueEmailCodeResponse, error) {
	c.request = request
	return &notificationv1.IssueEmailCodeResponse{Sent: true, ExpiresInSeconds: 600}, nil
}

func (c *recordingNotificationClient) VerifyEmailCode(context.Context, *notificationv1.VerifyEmailCodeRequest, ...grpc.CallOption) (*notificationv1.VerifyEmailCodeResponse, error) {
	return &notificationv1.VerifyEmailCodeResponse{}, nil
}

func TestNotificationCodeIssuerMapsRegistrationRequest(t *testing.T) {
	client := &recordingNotificationClient{}
	issuer := NewNotificationCodeIssuer(client)

	if err := issuer.Issue(context.Background(), "reader@example.com", RegistrationPurpose, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	if client.request.GetRecipient() != "reader@example.com" || client.request.GetPurpose() != RegistrationPurpose || client.request.GetLocale() != "zh-CN" {
		t.Fatalf("unexpected notification request: %+v", client.request)
	}
}
