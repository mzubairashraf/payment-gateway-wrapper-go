package payment

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func TestCardValidate(t *testing.T) {
	valid := Card{Number: "4111111111111111", ExpMonth: 12, ExpYear: 2030, CVV: "123"}
	tests := []struct {
		name      string
		mutate    func(*Card)
		wantField string // empty means valid
	}{
		{"valid visa", func(*Card) {}, ""},
		{"valid amex with 4-digit cvv", func(c *Card) { c.Number, c.CVV = "378282246310005", "1234" }, ""},
		{"expires this month", func(c *Card) { c.ExpMonth, c.ExpYear = 11, 2026 }, ""},
		{"expired last month", func(c *Card) { c.ExpMonth, c.ExpYear = 10, 2026 }, "card.exp_year"},
		{"expired last year", func(c *Card) { c.ExpYear = 2025 }, "card.exp_year"},
		{"two-digit year", func(c *Card) { c.ExpYear = 30 }, "card.exp_year"},
		{"month 13", func(c *Card) { c.ExpMonth = 13 }, "card.exp_month"},
		{"checksum typo", func(c *Card) { c.Number = "4111111111111112" }, "card.number"},
		{"too short", func(c *Card) { c.Number = "41111111" }, "card.number"},
		{"spaces not accepted", func(c *Card) { c.Number = "4111 1111 1111 1111" }, "card.number"},
		{"letters", func(c *Card) { c.Number = "4111abcd11111111" }, "card.number"},
		{"empty number", func(c *Card) { c.Number = "" }, "card.number"},
		{"cvv too short", func(c *Card) { c.CVV = "12" }, "card.cvv"},
		{"cvv not digits", func(c *Card) { c.CVV = "12a" }, "card.cvv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := valid
			tt.mutate(&card)
			err := card.Validate(now)
			switch {
			case tt.wantField == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantField != "" && (err == nil || err.Field != tt.wantField || err.Kind != KindInvalidRequest):
				t.Fatalf("err = %v, want invalid %s", err, tt.wantField)
			}
			if err != nil && strings.Contains(err.Error(), card.Number) && card.Number != "" {
				t.Errorf("validation error echoes the card number: %v", err)
			}
		})
	}
}

func TestCardNeverPrintsItself(t *testing.T) {
	card := Card{Number: "4111111111111111", ExpMonth: 12, ExpYear: 2030, CVV: "987"}
	req := ChargeRequest{Amount: 100, Currency: "USD", Card: card}

	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("charge", "card", card, "request", req)

	for name, out := range map[string]string{
		"%v":       fmt.Sprintf("%v", card),
		"%+v":      fmt.Sprintf("%+v", card),
		"%#v":      fmt.Sprintf("%#v", card),
		"%s":       fmt.Sprintf("%s", card),
		"%v req":   fmt.Sprintf("%v", req),
		"%+v req":  fmt.Sprintf("%+v", req),
		"%+v &req": fmt.Sprintf("%+v", &req),
		"slog":     logs.String(),
	} {
		if strings.Contains(out, card.Number) || strings.Contains(out, "987") {
			t.Errorf("%s leaks card data: %s", name, out)
		}
	}
	if got := card.String(); got != "card(****1111)" {
		t.Errorf("String() = %q", got)
	}
}

func TestFormatMinor(t *testing.T) {
	for amount, want := range map[int64]string{1: "0.01", 10: "0.10", 100: "1.00", 1050: "10.50", 123456789: "1234567.89"} {
		if got := FormatMinor(amount); got != want {
			t.Errorf("FormatMinor(%d) = %q, want %q", amount, got, want)
		}
	}
}

type stubGateway struct{ Gateway }

func (stubGateway) Name() string { return "AuthorizeNet" }

func TestRegistry(t *testing.T) {
	r := NewRegistry(stubGateway{})
	if _, err := r.Get("authorizenet"); err != nil {
		t.Errorf("lookup must be case-insensitive: %v", err)
	}
	for _, name := range []string{"", "vantiv"} {
		_, err := r.Get(name)
		pe, ok := err.(*Error)
		if !ok || pe.Kind != KindInvalidRequest || pe.Field != "gateway" || !strings.Contains(pe.Message, "authorizenet") {
			t.Errorf("Get(%q) error = %v, want invalid gateway listing the available names", name, err)
		}
	}
}
