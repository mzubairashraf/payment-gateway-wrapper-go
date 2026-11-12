// Package httpapi exposes the gateways over a small JSON API.
package httpapi

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/payment"
)

const (
	maxBodyBytes   = 16 << 10
	idempotencyTTL = 24 * time.Hour
)

var (
	currencyRe       = regexp.MustCompile(`^[A-Z]{3}$`)
	idempotencyKeyRe = regexp.MustCompile(`^[\x21-\x7e]{8,128}$`)
	last4Re          = regexp.MustCompile(`^[0-9]{4}$`)
)

// Server is the HTTP API.
type Server struct {
	gateways *payment.Registry
	token    string
	logger   *slog.Logger
	now      func() time.Time
	idem     *idempotencyStore
}

// Options configures a Server.
type Options struct {
	Gateways *payment.Registry
	APIToken string           // bearer token every /v1 request must present
	Logger   *slog.Logger     // default: discard
	Now      func() time.Time // default: time.Now
}

func New(opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Server{
		gateways: opts.Gateways, token: opts.APIToken, logger: opts.Logger, now: opts.Now,
		idem: newIdempotencyStore(idempotencyTTL, opts.Now),
	}
}

// Handler returns the routed, middleware-wrapped handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Handle("GET /v1/gateways", s.authenticated(http.HandlerFunc(s.listGateways)))
	mux.Handle("POST /v1/charges", s.authenticated(s.idempotent(s.createCharge)))
	mux.Handle("POST /v1/refunds", s.authenticated(s.idempotent(s.createRefund)))
	return s.observed(mux)
}

// ---- request and response bodies ----

type cardBody struct {
	Number   string `json:"number"`
	ExpMonth int    `json:"exp_month"`
	ExpYear  int    `json:"exp_year"`
	CVV      string `json:"cvv"`
}

type chargeBody struct {
	Gateway     string   `json:"gateway"`
	Amount      int64    `json:"amount"`
	Currency    string   `json:"currency"`
	Card        cardBody `json:"card"`
	Description string   `json:"description"`
	Reference   string   `json:"reference"`
}

type refundBody struct {
	Gateway       string `json:"gateway"`
	TransactionID string `json:"transaction_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	CardLast4     string `json:"card_last4"`
}

type cardView struct {
	Brand string `json:"brand,omitempty"`
	Last4 string `json:"last4"`
}

type chargeView struct {
	TransactionID string   `json:"transaction_id"`
	Gateway       string   `json:"gateway"`
	Status        string   `json:"status"`
	AuthCode      string   `json:"auth_code,omitempty"`
	Amount        int64    `json:"amount"`
	Currency      string   `json:"currency"`
	Card          cardView `json:"card"`
}

type refundView struct {
	TransactionID       string `json:"transaction_id"`
	Gateway             string `json:"gateway"`
	Status              string `json:"status"`
	ChargeTransactionID string `json:"charge_transaction_id"`
	Amount              int64  `json:"amount"`
	Currency            string `json:"currency"`
}

type errorView struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Field       string `json:"field,omitempty"`
	GatewayCode string `json:"gateway_code,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

// ---- handlers ----

func (s *Server) listGateways(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"gateways": s.gateways.Names()})
}

func (s *Server) createCharge(r *http.Request, raw []byte) (int, any) {
	var body chargeBody
	if err := decodeStrict(raw, &body); err != nil {
		return failure(err)
	}
	gateway, err := s.gateways.Get(body.Gateway)
	if err != nil {
		return failure(err)
	}
	card := payment.Card{Number: body.Card.Number, ExpMonth: body.Card.ExpMonth, ExpYear: body.Card.ExpYear, CVV: body.Card.CVV}
	if err := firstInvalid(
		validateMoney(body.Amount, body.Currency),
		card.Validate(s.now()),
		maxLen("description", body.Description, 255),
		maxLen("reference", body.Reference, 20),
	); err != nil {
		return failure(err)
	}

	charge, err := gateway.Charge(r.Context(), payment.ChargeRequest{
		Amount: body.Amount, Currency: body.Currency, Card: card,
		Description: body.Description, Reference: body.Reference,
	})
	if err != nil {
		return failure(err)
	}
	return http.StatusCreated, chargeView{
		TransactionID: charge.TransactionID, Gateway: gateway.Name(), Status: charge.Status,
		AuthCode: charge.AuthCode, Amount: charge.Amount, Currency: charge.Currency,
		Card: cardView{Brand: charge.CardBrand, Last4: charge.CardLast4},
	}
}

func (s *Server) createRefund(r *http.Request, raw []byte) (int, any) {
	var body refundBody
	if err := decodeStrict(raw, &body); err != nil {
		return failure(err)
	}
	gateway, err := s.gateways.Get(body.Gateway)
	if err != nil {
		return failure(err)
	}
	var idErr, last4Err *payment.Error
	if body.TransactionID == "" || len(body.TransactionID) > 64 {
		idErr = payment.Invalid("transaction_id", "transaction_id of the original charge is required")
	}
	if body.CardLast4 != "" && !last4Re.MatchString(body.CardLast4) {
		last4Err = payment.Invalid("card_last4", "card_last4 must be exactly four digits")
	}
	if err := firstInvalid(idErr, validateMoney(body.Amount, body.Currency), last4Err); err != nil {
		return failure(err)
	}

	refund, err := gateway.Refund(r.Context(), payment.RefundRequest{
		TransactionID: body.TransactionID, Amount: body.Amount, Currency: body.Currency, CardLast4: body.CardLast4,
	})
	if err != nil {
		return failure(err)
	}
	return http.StatusCreated, refundView{
		TransactionID: refund.TransactionID, Gateway: gateway.Name(), Status: "refunded",
		ChargeTransactionID: body.TransactionID, Amount: refund.Amount, Currency: refund.Currency,
	}
}

// ---- validation ----

func validateMoney(amount int64, currency string) *payment.Error {
	switch {
	case amount <= 0:
		return payment.Invalid("amount", "amount must be a positive integer in minor units (1050 = 10.50)")
	case amount > 99_999_999:
		return payment.Invalid("amount", "amount exceeds the supported maximum")
	case !currencyRe.MatchString(currency):
		return payment.Invalid("currency", "currency must be a three-letter ISO 4217 code in upper case")
	}
	return nil
}

func maxLen(field, value string, max int) *payment.Error {
	if len(value) > max {
		return payment.Invalid(field, field+" is too long")
	}
	return nil
}

func firstInvalid(errs ...*payment.Error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// decodeStrict rejects unknown fields and trailing data, so a misspelt field
// name is an error instead of a silently ignored value.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return payment.Invalid(typeErr.Field, "wrong JSON type for "+typeErr.Field)
		}
		if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return payment.Invalid(strings.Trim(name, `"`), "unknown field")
		}
		return payment.Invalid("", "request body is not valid JSON")
	}
	if dec.More() {
		return payment.Invalid("", "request body must contain a single JSON object")
	}
	return nil
}

// ---- errors ----

// failure maps an error onto an HTTP status and body. The status tells a
// client what to do without parsing the body:
//
//	400 fix the request      402 card declined       422 gateway refused it
//	503 not delivered, retry 504 outcome unknown, reconcile before retrying
func failure(err error) (int, any) {
	var pe *payment.Error
	if !errors.As(err, &pe) {
		return http.StatusInternalServerError, errorView{Code: "internal_error", Message: "internal error"}
	}
	view := errorView{Code: string(pe.Kind), Message: pe.Message, Field: pe.Field, GatewayCode: pe.Code}
	switch pe.Kind {
	case payment.KindInvalidRequest:
		return http.StatusBadRequest, view
	case payment.KindDeclined:
		return http.StatusPaymentRequired, view
	case payment.KindGatewayRejected:
		return http.StatusUnprocessableEntity, view
	case payment.KindGatewayUnavailable:
		return http.StatusServiceUnavailable, view
	case payment.KindOutcomeUnknown:
		return http.StatusGatewayTimeout, view
	default:
		return http.StatusInternalServerError, errorView{Code: "internal_error", Message: "internal error"}
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		status, body = http.StatusInternalServerError, []byte(`{"error":{"code":"internal_error","message":"internal error"}}`)
	}
	writeRaw(w, status, body)
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

func writeError(w http.ResponseWriter, status int, view errorView) {
	view.RequestID = w.Header().Get("X-Request-Id")
	writeJSON(w, status, map[string]any{"error": view})
}

// ---- middleware ----

// authenticated requires the configured bearer token.
func (s *Server) authenticated(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, errorView{Code: "unauthorized", Message: "missing or invalid bearer token"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// idempotent makes a write handler safe to retry. The handler runs at most
// once per Idempotency-Key; repeats receive the stored response.
func (s *Server) idempotent(handle func(r *http.Request, body []byte) (int, any)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if !idempotencyKeyRe.MatchString(key) {
			writeError(w, http.StatusBadRequest, errorView{Code: "invalid_request", Field: "Idempotency-Key",
				Message: "an Idempotency-Key header of 8 to 128 printable characters is required on writes"})
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, errorView{Code: "invalid_request", Message: "request body is too large"})
			return
		}

		state, stored := s.idem.begin(key, sha256.Sum256(append([]byte(r.URL.Path+"\n"), raw...)))
		switch state {
		case stateReplay:
			w.Header().Set("Idempotent-Replayed", "true")
			writeRaw(w, stored.status, stored.body)
			return
		case stateInProgress:
			writeError(w, http.StatusConflict, errorView{Code: "request_in_progress", Message: "a request with this Idempotency-Key is still being processed"})
			return
		case stateMismatch:
			writeError(w, http.StatusUnprocessableEntity, errorView{Code: "idempotency_key_reused", Message: "this Idempotency-Key was already used with a different request"})
			return
		}

		status, payload := handle(r, raw)
		if view, isErr := payload.(errorView); isErr {
			view.RequestID = w.Header().Get("X-Request-Id")
			payload = map[string]any{"error": view}
		}
		body, err := json.Marshal(payload)
		if err != nil {
			s.idem.release(key)
			writeError(w, http.StatusInternalServerError, errorView{Code: "internal_error", Message: "internal error"})
			return
		}

		// 400 and 503 mean nothing reached a gateway, so the key is freed
		// and the client may correct or retry. Everything else is final for
		// this key, including 504: after an unknown outcome a retry must
		// not send a second charge.
		if status == http.StatusBadRequest || status == http.StatusServiceUnavailable {
			s.idem.release(key)
		} else {
			s.idem.finish(key, status, body)
		}
		writeRaw(w, status, body)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// observed assigns a request ID, recovers panics and writes one access log
// line per request. Bodies and headers are never logged: they carry card
// data and credentials.
func (s *Server) observed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := newRequestID()
		w.Header().Set("X-Request-Id", requestID)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()

		defer func() {
			if p := recover(); p != nil {
				s.logger.Error("panic", slog.String("request_id", requestID), slog.Any("panic", p))
				writeError(rec, http.StatusInternalServerError, errorView{Code: "internal_error", Message: "internal error"})
			}
			s.logger.Info("request",
				slog.String("request_id", requestID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			)
		}()
		next.ServeHTTP(rec, r)
	})
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "req_" + hex.EncodeToString(b[:])
}
