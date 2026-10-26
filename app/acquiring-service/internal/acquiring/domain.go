package acquiring

import (
	"errors"
	"fmt"
)

type Side string

const (
	Debit  Side = "debit"
	Credit Side = "credit"
)

type Posting struct {
	Account string
	Side    Side
	Amount  int64
}

type Journal struct {
	BusinessType string
	BusinessID   string
	EventType    string
	Currency     string
	Postings     []Posting
}

type CaptureCommand struct {
	PaymentID, Currency string
	AmountMinor         int64
	FeeMinor            int64
}

type RefundCommand struct {
	RefundID, PaymentID, Currency string
	AmountMinor, FeeMinor         int64
}

type ChannelNetSettlementCommand struct {
	SettlementID, Currency string
	AmountMinor            int64
}

type ChannelCashSettlementCommand struct {
	SettlementID, Currency string
	AmountMinor            int64
}

type MerchantPayoutCommand struct {
	PayoutID, Currency string
	AmountMinor        int64
}

var ErrInvalidMoney = errors.New("invalid money")

func CaptureJournal(command CaptureCommand) (Journal, error) {
	if err := validateMoney(command.Currency, command.AmountMinor, command.FeeMinor); err != nil {
		return Journal{}, err
	}
	merchantNet := command.AmountMinor - command.FeeMinor
	return balanced(Journal{
		BusinessType: "merchant_payment",
		BusinessID:   command.PaymentID,
		EventType:    "capture",
		Currency:     command.Currency,
		Postings: []Posting{
			{Account: "channel_receivable", Side: Debit, Amount: command.AmountMinor},
			{Account: "merchant_payable", Side: Credit, Amount: merchantNet},
			{Account: "fee_revenue", Side: Credit, Amount: command.FeeMinor},
		},
	})
}

func RefundJournal(command RefundCommand) (Journal, error) {
	if err := validateMoney(command.Currency, command.AmountMinor, command.FeeMinor); err != nil {
		return Journal{}, err
	}
	merchantNet := command.AmountMinor - command.FeeMinor
	return balanced(Journal{
		BusinessType: "merchant_refund",
		BusinessID:   command.RefundID,
		EventType:    "refund",
		Currency:     command.Currency,
		Postings: []Posting{
			{Account: "merchant_payable", Side: Debit, Amount: merchantNet},
			{Account: "fee_revenue", Side: Debit, Amount: command.FeeMinor},
			{Account: "provider_refund_payable", Side: Credit, Amount: command.AmountMinor},
		},
	})
}

func ChannelNetSettlementJournal(command ChannelNetSettlementCommand) (Journal, error) {
	if command.Currency != "USD" || command.AmountMinor <= 0 {
		return Journal{}, ErrInvalidMoney
	}
	return balanced(Journal{
		BusinessType: "channel_net_settlement",
		BusinessID:   command.SettlementID,
		EventType:    "channel_settlement",
		Currency:     command.Currency,
		Postings: []Posting{
			{Account: "provider_refund_payable", Side: Debit, Amount: command.AmountMinor},
			{Account: "channel_receivable", Side: Credit, Amount: command.AmountMinor},
		},
	})
}

func ChannelCashSettlementJournal(command ChannelCashSettlementCommand) (Journal, error) {
	if command.Currency != "USD" || command.AmountMinor <= 0 {
		return Journal{}, ErrInvalidMoney
	}
	return balanced(Journal{
		BusinessType: "channel_cash_settlement",
		BusinessID:   command.SettlementID,
		EventType:    "channel_settlement",
		Currency:     command.Currency,
		Postings: []Posting{
			{Account: "platform_cash", Side: Debit, Amount: command.AmountMinor},
			{Account: "channel_receivable", Side: Credit, Amount: command.AmountMinor},
		},
	})
}

func MerchantPayoutJournal(command MerchantPayoutCommand) (Journal, error) {
	if command.Currency != "USD" || command.AmountMinor <= 0 {
		return Journal{}, ErrInvalidMoney
	}
	return balanced(Journal{
		BusinessType: "merchant_payout",
		BusinessID:   command.PayoutID,
		EventType:    "merchant_payout",
		Currency:     command.Currency,
		Postings: []Posting{
			{Account: "merchant_payable", Side: Debit, Amount: command.AmountMinor},
			{Account: "platform_cash", Side: Credit, Amount: command.AmountMinor},
		},
	})
}

func validateMoney(currency string, amount, fee int64) error {
	if currency != "USD" || amount <= 0 || fee < 0 || fee >= amount {
		return ErrInvalidMoney
	}
	return nil
}

func balanced(journal Journal) (Journal, error) {
	var debits, credits int64
	for _, posting := range journal.Postings {
		if posting.Amount <= 0 {
			return Journal{}, ErrInvalidMoney
		}
		switch posting.Side {
		case Debit:
			debits += posting.Amount
		case Credit:
			credits += posting.Amount
		default:
			return Journal{}, fmt.Errorf("unknown posting side %q", posting.Side)
		}
	}
	if len(journal.Postings) < 2 || debits != credits {
		return Journal{}, fmt.Errorf("unbalanced journal: debit=%d credit=%d", debits, credits)
	}
	return journal, nil
}
