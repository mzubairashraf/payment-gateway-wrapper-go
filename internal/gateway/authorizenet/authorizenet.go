// Package authorizenet adapts the Authorize.Net JSON API to payment.Gateway.
//
// Three properties of this API shape the adapter:
//
//   - It answers HTTP 200 for almost everything, including declines and
//     authentication failures. The outcome is in the body, on two levels:
//     messages.resultCode for the request and transactionResponse.responseCode
//     for the transaction.
//   - JSON requests are translated to XML on the server, so the order of
//     object keys is significant. The request structs below declare fields
//     in the order the API requires; do not sort them.
//   - It has no idempotency key. A charge whose response is lost cannot be
//     retried safely, so this adapter never retries and reports such
//     failures as payment.KindOutcomeUnknown.
package authorizenet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/payment"
)

const (
	// SandboxEndpoint is the Authorize.Net sandbox.
	SandboxEndpoint = "https://apitest.authorize.net/xml/v1/request.api"

	gatewayName      = "authorizenet"
	maxResponseBytes = 1 << 20
)

// Config holds the merchant credentials and endpoint.
type Config struct {
	APILoginID     string
	TransactionKey string
	Endpoint       string        // default SandboxEndpoint
	Currency       string        // the merchant account's currency; default USD
	Timeout        time.Duration // per request; default 20s
	HTTPClient     *http.Client  // default http.DefaultClient semantics with Timeout applied via context
}

// Gateway is the Authorize.Net adapter.
type Gateway struct {
	cfg Config
}

// New returns an adapter for one merchant account.
func New(cfg Config) *Gateway {
	if cfg.Endpoint == "" {
		cfg.Endpoint = SandboxEndpoint
	}
	if cfg.Currency == "" {
		cfg.Currency = "USD"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	return &Gateway{cfg: cfg}
}

func (g *Gateway) Name() string { return gatewayName }

// ---- wire format (field order is significant) ----

type request struct {
	CreateTransactionRequest createTransactionRequest `json:"createTransactionRequest"`
}

type createTransactionRequest struct {
	MerchantAuthentication merchantAuthentication `json:"merchantAuthentication"`
	RefID                  string                 `json:"refId,omitempty"`
	TransactionRequest     transactionRequest     `json:"transactionRequest"`
}

type merchantAuthentication struct {
	Name           string `json:"name"`
	TransactionKey string `json:"transactionKey"`
}

type transactionRequest struct {
	TransactionType string     `json:"transactionType"`
	Amount          string     `json:"amount"`
	Payment         paymentReq `json:"payment"`
	RefTransID      string     `json:"refTransId,omitempty"`
	Order           *order     `json:"order,omitempty"`
}

type paymentReq struct {
	CreditCard creditCard `json:"creditCard"`
}

type creditCard struct {
	CardNumber     string `json:"cardNumber"`
	ExpirationDate string `json:"expirationDate"`
	CardCode       string `json:"cardCode,omitempty"`
}

type order struct {
	Description string `json:"description"`
}

type response struct {
	TransactionResponse *struct {
		ResponseCode  string `json:"responseCode"`
		AuthCode      string `json:"authCode"`
		TransID       string `json:"transId"`
		AccountNumber string `json:"accountNumber"`
		AccountType   string `json:"accountType"`
		Errors        []struct {
			ErrorCode string `json:"errorCode"`
			ErrorText string `json:"errorText"`
		} `json:"errors"`
	} `json:"transactionResponse"`
	Messages struct {
		ResultCode string `json:"resultCode"`
		Message    []struct {
			Code string `json:"code"`
			Text string `json:"text"`
		} `json:"message"`
	} `json:"messages"`
}

// Transaction response codes.
const (
	codeApproved = "1"
	codeDeclined = "2"
	codeError    = "3"
	codeHeld     = "4"
)

// Charge authorises and captures in one step.
func (g *Gateway) Charge(ctx context.Context, req payment.ChargeRequest) (*payment.Charge, error) {
	if err := g.checkCurrency(req.Currency); err != nil {
		return nil, err
	}
	tx := transactionRequest{
		TransactionType: "authCaptureTransaction",
		Amount:          payment.FormatMinor(req.Amount),
		Payment: paymentReq{CreditCard: creditCard{
			CardNumber:     req.Card.Number,
			ExpirationDate: req.Card.Expiry(),
			CardCode:       req.Card.CVV,
		}},
	}
	if req.Description != "" {
		tx.Order = &order{Description: req.Description}
	}
	resp, err := g.send(ctx, req.Reference, tx)
	if err != nil {
		return nil, err
	}
	t := resp.TransactionResponse
	charge := &payment.Charge{
		TransactionID: t.TransID,
		Status:        payment.StatusApproved,
		AuthCode:      t.AuthCode,
		CardBrand:     t.AccountType,
		CardLast4:     req.Card.Last4(),
		Amount:        req.Amount,
		Currency:      g.cfg.Currency,
	}
	if t.ResponseCode == codeHeld {
		charge.Status = payment.StatusHeldForReview
	}
	return charge, nil
}

// Refund returns funds from a settled charge. Authorize.Net identifies the
// card by its last four digits and rejects refunds of charges that have not
// settled yet (error 54); those must be voided instead.
func (g *Gateway) Refund(ctx context.Context, req payment.RefundRequest) (*payment.Refund, error) {
	if err := g.checkCurrency(req.Currency); err != nil {
		return nil, err
	}
	if len(req.CardLast4) != 4 {
		return nil, payment.Invalid("card_last4", "card_last4 is required to refund through authorizenet")
	}
	resp, err := g.send(ctx, "", transactionRequest{
		TransactionType: "refundTransaction",
		Amount:          payment.FormatMinor(req.Amount),
		Payment:         paymentReq{CreditCard: creditCard{CardNumber: req.CardLast4, ExpirationDate: "XXXX"}},
		RefTransID:      req.TransactionID,
	})
	if err != nil {
		return nil, err
	}
	return &payment.Refund{TransactionID: resp.TransactionResponse.TransID, Amount: req.Amount, Currency: g.cfg.Currency}, nil
}

func (g *Gateway) checkCurrency(currency string) error {
	if currency != g.cfg.Currency {
		return payment.Invalid("currency", "this authorizenet account settles in "+g.cfg.Currency+" only")
	}
	return nil
}

// send performs one request and returns the response only when the
// transaction was accepted (approved or held). Every other outcome is a
// classified *payment.Error.
func (g *Gateway) send(ctx context.Context, refID string, tx transactionRequest) (*response, error) {
	body, err := json.Marshal(request{CreateTransactionRequest: createTransactionRequest{
		MerchantAuthentication: merchantAuthentication{Name: g.cfg.APILoginID, TransactionKey: g.cfg.TransactionKey},
		RefID:                  refID,
		TransactionRequest:     tx,
	}})
	if err != nil {
		return nil, &payment.Error{Kind: payment.KindInvalidRequest, Message: "request could not be encoded"}
	}

	ctx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &payment.Error{Kind: payment.KindGatewayUnavailable, Message: "gateway endpoint is misconfigured"}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := g.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, transportError(err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBytes))
	if err != nil {
		return nil, unknownOutcome("the gateway's response was cut off")
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, unknownOutcome("the gateway answered HTTP " + http.StatusText(httpResp.StatusCode))
	}

	// Authorize.Net prefixes its JSON with a UTF-8 byte order mark, which
	// encoding/json rejects.
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, unknownOutcome("the gateway's response could not be read")
	}
	return &resp, classify(&resp)
}

// classify turns the two-level result into nil (accepted) or an error.
func classify(resp *response) error {
	t := resp.TransactionResponse
	if t == nil {
		// The request itself was refused (bad credentials, malformed
		// request); no transaction was attempted.
		e := &payment.Error{Kind: payment.KindGatewayRejected, Message: "the gateway rejected the request"}
		if len(resp.Messages.Message) > 0 {
			e.Code, e.Message = resp.Messages.Message[0].Code, resp.Messages.Message[0].Text
		}
		return e
	}

	switch t.ResponseCode {
	case codeApproved, codeHeld:
		if t.TransID == "" || t.TransID == "0" {
			return unknownOutcome("the gateway reported success without a transaction id")
		}
		return nil
	case codeDeclined:
		e := &payment.Error{Kind: payment.KindDeclined, Message: "the card was declined"}
		if len(t.Errors) > 0 {
			e.Code, e.Message = t.Errors[0].ErrorCode, t.Errors[0].ErrorText
		}
		return e
	case codeError:
		e := &payment.Error{Kind: payment.KindGatewayRejected, Message: "the gateway could not process the transaction"}
		if len(t.Errors) > 0 {
			e.Code, e.Message = t.Errors[0].ErrorCode, t.Errors[0].ErrorText
		}
		return e
	default:
		return unknownOutcome("the gateway returned an unrecognised response code")
	}
}

// transportError separates "never delivered" from "delivered, no answer".
// Only a failure to connect proves the gateway did not see the request.
func transportError(err error) *payment.Error {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return &payment.Error{Kind: payment.KindGatewayUnavailable, Message: "could not connect to the gateway"}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return &payment.Error{Kind: payment.KindGatewayUnavailable, Message: "could not resolve the gateway host"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return unknownOutcome("the gateway did not answer in time")
	}
	return unknownOutcome("the connection to the gateway failed mid-request")
}

func unknownOutcome(message string) *payment.Error {
	return &payment.Error{Kind: payment.KindOutcomeUnknown, Message: message}
}
