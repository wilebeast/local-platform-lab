package registration

import (
	"context"
	"errors"

	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
)

type NotificationCodeIssuer struct {
	client notificationv1.NotificationServiceClient
}

func NewNotificationCodeIssuer(client notificationv1.NotificationServiceClient) *NotificationCodeIssuer {
	return &NotificationCodeIssuer{client: client}
}

func (i *NotificationCodeIssuer) Issue(ctx context.Context, email, purpose, locale string) error {
	if i == nil || i.client == nil {
		return errors.New("notification client is required")
	}
	_, err := i.client.IssueEmailCode(ctx, &notificationv1.IssueEmailCodeRequest{
		Recipient: email,
		Purpose:   purpose,
		Locale:    locale,
	})
	return err
}
