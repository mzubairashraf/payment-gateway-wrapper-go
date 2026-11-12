package authorizenet

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/payment"
)

const bom = "\xef\xbb\xbf"

var (
	card      = payment.Card{Number: "4111111111111111", ExpMonth: 12, ExpYear: 2030, CVV: "123"}
	chargeReq = payment.ChargeRequest{Amount: 1050, Currency: "USD", Card: card, Description: "Order 7", Reference: "ord-7"}
)

// fake serves one canned response and records the request body.
func fake(t *testing.T, status int, body string) (*Gateway, *string) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = string(raw)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(Config{APILoginID: "login-id", TransactionKey: "txn-key", Endpoint: srv.URL}), &got
}

func TestChargeOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		httpStatus int
		body       string
		wantKind   payment.Kind // empty means accepted
		wantCode   string
		wantStatus string
	}{
		{name: "approved, with the byte order mark Authorize.Net sends", httpStatus: 200,
			body:       bom + `{"transactionResponse":{"responseCode":"1","authCode":"HW617E","transId":"60012345","accountNumber":"XXXX1111","accountType":"Visa"},"messages":{"resultCode":"Ok","message":[{"code":"I00001","text":"Successful."}]}}`,
			wantStatus: payment.StatusApproved},
		{name: "held for review", httpStatus: 200,
			body:       `{"transactionResponse":{"responseCode":"4","transId":"60012346","accountType":"Visa"},"messages":{"resultCode":"Ok"}}`,
			wantStatus: payment.StatusHeldForReview},
		{name: "declined", httpStatus: 200,
			body:     `{"transactionResponse":{"responseCode":"2","transId":"60012347","errors":[{"errorCode":"2","errorText":"This transaction has been declined."}]},"messages":{"resultCode":"Ok"}}`,
			wantKind: payment.KindDeclined, wantCode: "2"},
		{name: "transaction error", httpStatus: 200,
			body:     `{"transactionResponse":{"responseCode":"3","transId":"0","errors":[{"errorCode":"11","errorText":"A duplicate transaction has been submitted."}]},"messages":{"resultCode":"Error","message":[{"code":"E00027","text":"The transaction was unsuccessful."}]}}`,
			wantKind: payment.KindGatewayRejected, wantCode: "11"},
		{name: "bad merchant credentials, no transaction attempted", httpStatus: 200,
			body:     `{"messages":{"resultCode":"Error","message":[{"code":"E00007","text":"User authentication failed due to invalid authentication values."}]}}`,
			wantKind: payment.KindGatewayRejected, wantCode: "E00007"},
		{name: "success without a transaction id is not trusted", httpStatus: 200,
			body:     `{"transactionResponse":{"responseCode":"1","transId":"0"},"messages":{"resultCode":"Ok"}}`,
			wantKind: payment.KindOutcomeUnknown},
		{name: "unrecognised response code", httpStatus: 200,
			body:     `{"transactionResponse":{"responseCode":"9","transId":"1"},"messages":{"resultCode":"Ok"}}`,
			wantKind: payment.KindOutcomeUnknown},
		{name: "HTTP 500", httpStatus: 500, body: `oops`, wantKind: payment.KindOutcomeUnknown},
		{name: "unreadable body", httpStatus: 200, body: `<html>maintenance</html>`, wantKind: payment.KindOutcomeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := fake(t, tt.httpStatus, tt.body)
			charge, err := g.Charge(context.Background(), chargeReq)

			if tt.wantKind == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if charge.Status != tt.wantStatus || charge.TransactionID == "" || charge.CardLast4 != "1111" ||
					charge.CardBrand != "Visa" || charge.Amount != 1050 || charge.Currency != "USD" {
					t.Errorf("charge = %+v", charge)
				}
				return
			}
			var pe *payment.Error
			if !errors.As(err, &pe) || pe.Kind != tt.wantKind || pe.Code != tt.wantCode {
				t.Fatalf("err = %v, want kind %s code %q", err, tt.wantKind, tt.wantCode)
			}
			if charge != nil {
				t.Error("a failed charge must not return a charge")
			}
			if strings.Contains(err.Error(), card.Number) || strings.Contains(err.Error(), "txn-key") {
				t.Errorf("error leaks sensitive data: %v", err)
			}
		})
	}
}

// Authorize.Net maps JSON onto an ordered XML schema, so key order is part
// of the contract. This test pins it.
func TestRequestShapeAndKeyOrder(t *testing.T) {
	const ok = `{"transactionResponse":{"responseCode":"1","transId":"1"},"messages":{"resultCode":"Ok"}}`
	inOrder := func(t *testing.T, body string, keys ...string) {
		t.Helper()
		pos := -1
		for _, k := range keys {
			i := strings.Index(body, k)
			if i < 0 {
				t.Fatalf("request is missing %s:\n%s", k, body)
			}
			if i < pos {
				t.Fatalf("%s is out of order:\n%s", k, body)
			}
			pos = i
		}
	}

	t.Run("charge", func(t *testing.T) {
		g, body := fake(t, 200, ok)
		if _, err := g.Charge(context.Background(), chargeReq); err != nil {
			t.Fatal(err)
		}
		inOrder(t, *body, `"merchantAuthentication"`, `"name":"login-id"`, `"transactionKey":"txn-key"`, `"refId":"ord-7"`,
			`"transactionRequest"`, `"transactionType":"authCaptureTransaction"`, `"amount":"10.50"`, `"payment"`,
			`"cardNumber":"4111111111111111"`, `"expirationDate":"2030-12"`, `"cardCode":"123"`, `"order"`, `"description":"Order 7"`)
	})

	t.Run("refund", func(t *testing.T) {
		g, body := fake(t, 200, ok)
		refund, err := g.Refund(context.Background(), payment.RefundRequest{TransactionID: "60012345", Amount: 500, Currency: "USD", CardLast4: "1111"})
		if err != nil {
			t.Fatal(err)
		}
		if refund.Amount != 500 || refund.TransactionID != "1" {
			t.Errorf("refund = %+v", refund)
		}
		inOrder(t, *body, `"transactionType":"refundTransaction"`, `"amount":"5.00"`, `"payment"`,
			`"cardNumber":"1111"`, `"expirationDate":"XXXX"`, `"refTransId":"60012345"`)
		if strings.Contains(*body, "cardCode") {
			t.Errorf("refund must not send a card code:\n%s", *body)
		}
	})
}

func TestRejectedBeforeSending(t *testing.T) {
	g, body := fake(t, 200, `{}`)
	tests := []struct {
		name      string
		call      func() error
		wantField string
	}{
		{"charge in another currency", func() error {
			req := chargeReq
			req.Currency = "EUR"
			_, err := g.Charge(context.Background(), req)
			return err
		}, "currency"},
		{"refund without last four", func() error {
			_, err := g.Refund(context.Background(), payment.RefundRequest{TransactionID: "1", Amount: 1, Currency: "USD"})
			return err
		}, "card_last4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pe *payment.Error
			if err := tt.call(); !errors.As(err, &pe) || pe.Kind != payment.KindInvalidRequest || pe.Field != tt.wantField {
				t.Fatalf("err = %v, want invalid %s", err, tt.wantField)
			}
			if *body != "" {
				t.Error("no request should have been sent")
			}
		})
	}
}

// The distinction these two tests pin is the one that prevents double
// charges: only a failure to connect proves the gateway never saw the request.
func TestTimeoutIsAnUnknownOutcome(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	g := New(Config{Endpoint: srv.URL, Timeout: 30 * time.Millisecond})
	_, err := g.Charge(context.Background(), chargeReq)
	var pe *payment.Error
	if !errors.As(err, &pe) || pe.Kind != payment.KindOutcomeUnknown {
		t.Fatalf("err = %v, want outcome_unknown", err)
	}
}

func TestConnectionRefusedIsSafeToRetry(t *testing.T) {
	g := New(Config{Endpoint: "http://127.0.0.1:1"})
	_, err := g.Charge(context.Background(), chargeReq)
	var pe *payment.Error
	if !errors.As(err, &pe) || pe.Kind != payment.KindGatewayUnavailable {
		t.Fatalf("err = %v, want gateway_unavailable", err)
	}
}
