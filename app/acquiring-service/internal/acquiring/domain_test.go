package acquiring

import (
	"errors"
	"reflect"
	"testing"
)

func TestSuccessfulHundredDollarPaymentCreatesMerchantAndFeeLiabilities(t *testing.T) {
	journal, err := CaptureJournal(CaptureCommand{
		PaymentID: "payment-100", Currency: "USD", AmountMinor: 10000, FeeMinor: 200,
	})
	if err != nil {
		t.Fatalf("capture journal: %v", err)
	}
	want := Journal{
		BusinessType: "merchant_payment", BusinessID: "payment-100", EventType: "capture", Currency: "USD",
		Postings: []Posting{
			{Account: "channel_receivable", Side: Debit, Amount: 10000},
			{Account: "merchant_payable", Side: Credit, Amount: 9800},
			{Account: "fee_revenue", Side: Credit, Amount: 200},
		},
	}
	if !reflect.DeepEqual(journal, want) {
		t.Fatalf("capture journal = %#v, want %#v", journal, want)
	}
}

func TestFullRefundBeforeMerchantPayoutReversesPayableAndFee(t *testing.T) {
	journal, err := RefundJournal(RefundCommand{
		RefundID: "refund-100", PaymentID: "payment-100", Currency: "USD", AmountMinor: 10000, FeeMinor: 200,
	})
	if err != nil {
		t.Fatalf("refund journal: %v", err)
	}
	want := []Posting{
		{Account: "merchant_payable", Side: Debit, Amount: 9800},
		{Account: "fee_revenue", Side: Debit, Amount: 200},
		{Account: "provider_refund_payable", Side: Credit, Amount: 10000},
	}
	if !reflect.DeepEqual(journal.Postings, want) {
		t.Fatalf("refund postings = %#v, want %#v", journal.Postings, want)
	}
}

func TestFeeCannotConsumeWholePayment(t *testing.T) {
	_, err := CaptureJournal(CaptureCommand{PaymentID: "payment-invalid", Currency: "USD", AmountMinor: 10000, FeeMinor: 10000})
	if !errors.Is(err, ErrInvalidMoney) {
		t.Fatalf("capture error = %v, want ErrInvalidMoney", err)
	}
}
