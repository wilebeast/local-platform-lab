package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	notificationv1 "github.com/wilebeast/local-platform-lab/api/notification/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func main() {
	address := flag.String("address", "127.0.0.1:19003", "gRPC address")
	action := flag.String("action", "send-template", "send-template, issue-code, or verify-code")
	recipient := flag.String("recipient", "reader@example.test", "recipient email")
	locale := flag.String("locale", "en", "template locale")
	purpose := flag.String("purpose", "sign_in", "verification purpose")
	code := flag.String("code", "", "verification code")
	flag.Parse()

	conn, err := grpc.NewClient(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	client := notificationv1.NewNotificationServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var response any
	switch *action {
	case "send-template":
		response, err = client.SendTemplatedEmail(ctx, &notificationv1.SendTemplatedEmailRequest{
			TemplateKey: "workspace_welcome", Locale: *locale, Recipient: *recipient,
			Variables:   map[string]string{"DisplayName": "Coach"},
			Attachments: []*notificationv1.Attachment{{Filename: "welcome.txt", ContentType: "text/plain", Content: []byte("Local notification lab attachment.\n")}},
		})
	case "issue-code":
		response, err = client.IssueEmailCode(ctx, &notificationv1.IssueEmailCodeRequest{Recipient: *recipient, Purpose: *purpose, Locale: *locale})
	case "verify-code":
		response, err = client.VerifyEmailCode(ctx, &notificationv1.VerifyEmailCodeRequest{Recipient: *recipient, Purpose: *purpose, Code: *code})
	default:
		log.Fatalf("unknown action %q", *action)
	}
	if err != nil {
		log.Fatal(err)
	}
	payload, err := protojson.MarshalOptions{Multiline: true, Indent: "  ", EmitUnpopulated: true}.Marshal(response.(proto.Message))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(payload))
}
