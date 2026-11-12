package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/gateway/simulator"
	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/payment"
)

const token = "test-token-0123456789"

// countingGateway wraps the simulator to count calls and optionally slow
// them down, so tests can assert how often a gateway was really invoked.
type countingGateway struct {
	payment.Gateway
	charges atomic.Int32
	delay   time.Duration
}

func (g *countingGateway) Charge(ctx context.Context, req payment.ChargeRequest) (*payment.Charge, error) {
	g.charges.Add(1)
	time.Sleep(g.delay)
	return g.Gateway.Charge(ctx, req)
}

type harness struct {
	t       *testing.T
	handler http.Handler
	gateway *countingGateway
	logs    *bytes.Buffer
	keys    int
}

func newHarness(t *testing.T) *harness {
	gw := &countingGateway{Gateway: simulator.New()}
	logs := &bytes.Buffer{}
	srv := New(Options{
		Gateways: payment.NewRegistry(gw), APIToken: token,
		// Timestamps are dropped so that digit runs in them cannot collide
		// with the card data the log-redaction test searches for.
		Logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey || a.Key == "duration_ms" {
				return slog.Attr{}
			}
			return a
		}})),
		Now: func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) },
	})
	return &harness{t: t, handler: srv.Handler(), gateway: gw, logs: logs}
}

type result struct {
	status int
	header http.Header
	body   map[string]any
}

func (r result) errorField(name string) any {
	e, _ := r.body["error"].(map[string]any)
	return e[name]
}

// post sends a JSON body. An empty key gets a fresh Idempotency-Key.
func (h *harness) post(path, key string, body any) result {
	h.t.Helper()
	raw, ok := body.(string)
	if !ok {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	if key == "" {
		h.keys++
		key = fmt.Sprintf("test-key-%04d", h.keys)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	if key != "none" {
		req.Header.Set("Idempotency-Key", key)
	}
	return h.do(req)
}

func (h *harness) do(req *http.Request) result {
	h.t.Helper()
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	res := result{status: rec.Code, header: rec.Header()}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &res.body); err != nil {
			h.t.Fatalf("response is not JSON: %s", rec.Body)
		}
	}
	return res
}

func charge(cardNumber string, amount int64) map[string]any {
	return map[string]any{
		"gateway": "simulator", "amount": amount, "currency": "USD",
		"card": map[string]any{"number": cardNumber, "exp_month": 12, "exp_year": 2030, "cvv": "123"},
	}
}

func TestChargeOutcomesMapToStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		card       string
		wantStatus int
		wantCode   string // error code; empty for success
		wantState  string
	}{
		{"approved", simulator.CardApproved, 201, "", "approved"},
		{"held for review", simulator.CardHeld, 201, "", "held_for_review"},
		{"declined", simulator.CardDeclined, 402, "card_declined", ""},
		{"gateway unreachable", simulator.CardDown, 503, "gateway_unavailable", ""},
		{"outcome unknown", simulator.CardUnknown, 504, "outcome_unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			res := h.post("/v1/charges", "", charge(tt.card, 1050))
			if res.status != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %v", res.status, tt.wantStatus, res.body)
			}
			if tt.wantCode != "" {
				if res.errorField("code") != tt.wantCode || res.errorField("request_id") != res.header.Get("X-Request-Id") {
					t.Errorf("error = %v", res.body)
				}
				return
			}
			card := res.body["card"].(map[string]any)
			if res.body["status"] != tt.wantState || res.body["transaction_id"] == "" || res.body["amount"] != 1050.0 ||
				card["last4"] != tt.card[len(tt.card)-4:] || card["brand"] != "Visa" {
				t.Errorf("body = %v", res.body)
			}
			if strings.Contains(fmt.Sprint(res.body), tt.card) {
				t.Error("response echoes the full card number")
			}
		})
	}
}

func TestChargeValidation(t *testing.T) {
	with := func(mutate func(map[string]any)) map[string]any {
		body := charge(simulator.CardApproved, 1050)
		mutate(body)
		return body
	}
	card := func(key string, value any) func(map[string]any) {
		return func(b map[string]any) { b["card"].(map[string]any)[key] = value }
	}
	tests := []struct {
		name      string
		body      any
		wantField string
	}{
		{"zero amount", with(func(b map[string]any) { b["amount"] = 0 }), "amount"},
		{"negative amount", with(func(b map[string]any) { b["amount"] = -100 }), "amount"},
		{"decimal amount", with(func(b map[string]any) { b["amount"] = 10.5 }), "amount"},
		{"amount as string", with(func(b map[string]any) { b["amount"] = "1050" }), "amount"},
		{"lower-case currency", with(func(b map[string]any) { b["currency"] = "usd" }), "currency"},
		{"missing gateway", with(func(b map[string]any) { delete(b, "gateway") }), "gateway"},
		{"unknown gateway", with(func(b map[string]any) { b["gateway"] = "Vantiv" }), "gateway"},
		{"card fails checksum", with(card("number", "4111111111111112")), "card.number"},
		{"card expired", with(card("exp_year", 2025)), "card.exp_year"},
		{"bad cvv", with(card("cvv", "12")), "card.cvv"},
		{"reference too long", with(func(b map[string]any) { b["reference"] = strings.Repeat("r", 21) }), "reference"},
		{"misspelt field", with(func(b map[string]any) { b["ammount"] = 5 }), "ammount"},
		{"not json", `{"gateway":`, ""},
		{"two objects", `{} {}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			res := h.post("/v1/charges", "", tt.body)
			if res.status != 400 || res.errorField("code") != "invalid_request" {
				t.Fatalf("status = %d body = %v, want 400 invalid_request", res.status, res.body)
			}
			if got, _ := res.errorField("field").(string); got != tt.wantField {
				t.Errorf("field = %q, want %q", got, tt.wantField)
			}
			if n := h.gateway.charges.Load(); n != 0 {
				t.Errorf("gateway was called %d times for an invalid request", n)
			}
		})
	}
}

func TestAuthentication(t *testing.T) {
	h := newHarness(t)
	for name, header := range map[string]string{"missing": "", "wrong": "Bearer nope", "wrong scheme": "Basic " + token} {
		for _, target := range []string{"POST /v1/charges", "POST /v1/refunds", "GET /v1/gateways"} {
			method, path, _ := strings.Cut(target, " ")
			req := httptest.NewRequest(method, path, strings.NewReader("{}"))
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			if res := h.do(req); res.status != 401 || res.errorField("code") != "unauthorized" {
				t.Errorf("%s token on %s: status %d body %v", name, target, res.status, res.body)
			}
		}
	}
	if res := h.do(httptest.NewRequest(http.MethodGet, "/healthz", nil)); res.status != 204 {
		t.Errorf("healthz = %d, want 204 without a token", res.status)
	}
}

func TestIdempotency(t *testing.T) {
	t.Run("a key is required on writes", func(t *testing.T) {
		h := newHarness(t)
		res := h.post("/v1/charges", "none", charge(simulator.CardApproved, 1050))
		if res.status != 400 || res.errorField("field") != "Idempotency-Key" {
			t.Errorf("status %d body %v", res.status, res.body)
		}
	})

	t.Run("a retry replays the first response and charges once", func(t *testing.T) {
		h := newHarness(t)
		first := h.post("/v1/charges", "order-1001-attempt", charge(simulator.CardApproved, 1050))
		second := h.post("/v1/charges", "order-1001-attempt", charge(simulator.CardApproved, 1050))
		if first.status != 201 || second.status != 201 || first.body["transaction_id"] != second.body["transaction_id"] {
			t.Errorf("first %v, second %v", first.body, second.body)
		}
		if second.header.Get("Idempotent-Replayed") != "true" || first.header.Get("Idempotent-Replayed") != "" {
			t.Error("only the replay must carry Idempotent-Replayed")
		}
		if n := h.gateway.charges.Load(); n != 1 {
			t.Errorf("gateway charged %d times, want 1", n)
		}
	})

	t.Run("a key reused for a different request is refused", func(t *testing.T) {
		h := newHarness(t)
		h.post("/v1/charges", "order-1002-attempt", charge(simulator.CardApproved, 1050))
		res := h.post("/v1/charges", "order-1002-attempt", charge(simulator.CardApproved, 9999))
		if res.status != 422 || res.errorField("code") != "idempotency_key_reused" || h.gateway.charges.Load() != 1 {
			t.Errorf("status %d body %v charges %d", res.status, res.body, h.gateway.charges.Load())
		}
	})

	t.Run("a decline is final for its key", func(t *testing.T) {
		h := newHarness(t)
		h.post("/v1/charges", "order-1003-attempt", charge(simulator.CardDeclined, 1050))
		res := h.post("/v1/charges", "order-1003-attempt", charge(simulator.CardDeclined, 1050))
		if res.status != 402 || h.gateway.charges.Load() != 1 {
			t.Errorf("status %d charges %d", res.status, h.gateway.charges.Load())
		}
	})

	t.Run("an unknown outcome is never retried under the same key", func(t *testing.T) {
		h := newHarness(t)
		h.post("/v1/charges", "order-1004-attempt", charge(simulator.CardUnknown, 1050))
		res := h.post("/v1/charges", "order-1004-attempt", charge(simulator.CardUnknown, 1050))
		if res.status != 504 || res.header.Get("Idempotent-Replayed") != "true" || h.gateway.charges.Load() != 1 {
			t.Errorf("status %d replayed %q charges %d, want one gateway call", res.status, res.header.Get("Idempotent-Replayed"), h.gateway.charges.Load())
		}
	})

	t.Run("an undelivered request frees its key for a retry", func(t *testing.T) {
		h := newHarness(t)
		h.post("/v1/charges", "order-1005-attempt", charge(simulator.CardDown, 1050))
		res := h.post("/v1/charges", "order-1005-attempt", charge(simulator.CardDown, 1050))
		if res.status != 503 || res.header.Get("Idempotent-Replayed") != "" || h.gateway.charges.Load() != 2 {
			t.Errorf("status %d charges %d, want a real second attempt", res.status, h.gateway.charges.Load())
		}
	})

	t.Run("a rejected request frees its key for the corrected request", func(t *testing.T) {
		h := newHarness(t)
		bad := h.post("/v1/charges", "order-1006-attempt", charge(simulator.CardApproved, 0))
		good := h.post("/v1/charges", "order-1006-attempt", charge(simulator.CardApproved, 1050))
		if bad.status != 400 || good.status != 201 {
			t.Errorf("bad %d, good %d", bad.status, good.status)
		}
	})

	t.Run("concurrent duplicates charge exactly once", func(t *testing.T) {
		h := newHarness(t)
		h.gateway.delay = 50 * time.Millisecond
		var wg sync.WaitGroup
		statuses := make([]int, 20)
		for i := range statuses {
			wg.Add(1)
			go func() {
				defer wg.Done()
				body, _ := json.Marshal(charge(simulator.CardApproved, 1050))
				req := httptest.NewRequest(http.MethodPost, "/v1/charges", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Idempotency-Key", "order-1007-attempt")
				rec := httptest.NewRecorder()
				h.handler.ServeHTTP(rec, req)
				statuses[i] = rec.Code
			}()
		}
		wg.Wait()

		created := 0
		for _, s := range statuses {
			switch s {
			case 201:
				created++
			case 409:
			default:
				t.Errorf("unexpected status %d", s)
			}
		}
		if n := h.gateway.charges.Load(); n != 1 || created < 1 {
			t.Errorf("gateway charged %d times (201s: %d), want exactly 1", n, created)
		}
	})
}

func TestRefundFlow(t *testing.T) {
	h := newHarness(t)
	charged := h.post("/v1/charges", "", charge(simulator.CardApproved, 5000))
	id := charged.body["transaction_id"]
	refund := func(amount int64) result {
		return h.post("/v1/refunds", "", map[string]any{"gateway": "simulator", "transaction_id": id, "amount": amount, "currency": "USD", "card_last4": "1111"})
	}

	if res := refund(2000); res.status != 201 || res.body["status"] != "refunded" || res.body["charge_transaction_id"] != id || res.body["amount"] != 2000.0 {
		t.Errorf("partial refund: %d %v", res.status, res.body)
	}
	if res := refund(3000); res.status != 201 {
		t.Errorf("refund of the remainder: %d %v", res.status, res.body)
	}
	if res := refund(1); res.status != 422 || res.errorField("code") != "gateway_rejected" || res.errorField("gateway_code") != "amount_too_large" {
		t.Errorf("over-refund: %d %v", res.status, res.body)
	}

	for name, body := range map[string]map[string]any{
		"transaction_id": {"gateway": "simulator", "amount": 1, "currency": "USD"},
		"card_last4":     {"gateway": "simulator", "transaction_id": "x", "amount": 1, "currency": "USD", "card_last4": "11"},
		"amount":         {"gateway": "simulator", "transaction_id": "x", "amount": 0, "currency": "USD"},
	} {
		if res := h.post("/v1/refunds", "", body); res.status != 400 || res.errorField("field") != name {
			t.Errorf("invalid %s: %d %v", name, res.status, res.body)
		}
	}
	if res := h.post("/v1/refunds", "", map[string]any{"gateway": "simulator", "transaction_id": "sim_ch_999999", "amount": 1, "currency": "USD"}); res.status != 422 {
		t.Errorf("unknown transaction: %d %v", res.status, res.body)
	}
}

func TestListGateways(t *testing.T) {
	h := newHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/gateways", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := h.do(req)
	if res.status != 200 || fmt.Sprint(res.body["gateways"]) != "[simulator]" {
		t.Errorf("status %d body %v", res.status, res.body)
	}
}

func TestOversizedBodyIsRefused(t *testing.T) {
	h := newHarness(t)
	res := h.post("/v1/charges", "", `{"description":"`+strings.Repeat("x", maxBodyBytes)+`"}`)
	if res.status != http.StatusRequestEntityTooLarge || h.gateway.charges.Load() != 0 {
		t.Errorf("status %d", res.status)
	}
}

func TestLogsNeverContainCardDataOrCredentials(t *testing.T) {
	h := newHarness(t)
	for _, number := range []string{simulator.CardApproved, simulator.CardDeclined, simulator.CardUnknown, "4111111111111112"} {
		body := charge(number, 1050)
		body["card"].(map[string]any)["cvv"] = "739"
		h.post("/v1/charges", "", body)
	}

	logs := h.logs.String()
	for _, secret := range []string{simulator.CardApproved, simulator.CardDeclined, simulator.CardUnknown, "4111111111111112", "739", token} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain %q:\n%s", secret, logs)
		}
	}
	if n := strings.Count(logs, `"msg":"request"`); n != 4 {
		t.Errorf("access log lines = %d, want 4:\n%s", n, logs)
	}
	for _, want := range []string{`"status":201`, `"status":402`, `"status":504`, `"status":400`, `"path":"/v1/charges"`, `"request_id":"req_`} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs missing %s", want)
		}
	}
}
