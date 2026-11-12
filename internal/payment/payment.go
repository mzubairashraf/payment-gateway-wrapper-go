// Package payment defines the gateway-neutral model: what a charge and a
// refund are, what a gateway must implement, and how failures are classified.
// It has no dependency on HTTP or on any specific gateway.
package payment

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Gateway is implemented once per payment provider.
//
// Implementations must return *Error for every failure and must classify it
// honestly: in particular, KindOutcomeUnknown is reserved for the case where
// the request may have reached the provider but no answer came back.
type Gateway interface {
	// Name is the identifier callers use to select this gateway.
	Name() string
	Charge(ctx context.Context, req ChargeRequest) (*Charge, error)
	Refund(ctx context.Context, req RefundRequest) (*Refund, error)
}

// ChargeRequest asks a gateway to authorise and capture a card payment.
type ChargeRequest struct {
	Amount      int64  // in the currency's minor unit, e.g. cents
	Currency    string // ISO 4217, upper case
	Card        Card
	Description string
	Reference   string // caller's own reference, at most 20 characters
}

// Status of a charge that the gateway accepted.
const (
	StatusApproved      = "approved"
	StatusHeldForReview = "held_for_review" // accepted but held by the gateway's fraud filters
)

// Charge is an accepted charge.
type Charge struct {
	TransactionID string // the gateway's identifier; needed to refund
	Status        string
	AuthCode      string
	CardBrand     string
	CardLast4     string
	Amount        int64
	Currency      string
}

// RefundRequest asks a gateway to return funds from an earlier charge.
type RefundRequest struct {
	TransactionID string // Charge.TransactionID of the original charge
	Amount        int64
	Currency      string
	CardLast4     string // required by some gateways to identify the card
}

// Refund is an accepted refund.
type Refund struct {
	TransactionID string // the refund's own identifier at the gateway
	Amount        int64
	Currency      string
}

// Kind classifies a failure by what the caller should do about it.
type Kind string

const (
	// KindInvalidRequest: the request is malformed. Fix it; do not retry as is.
	KindInvalidRequest Kind = "invalid_request"
	// KindDeclined: the issuer or gateway declined the card. No money moved.
	KindDeclined Kind = "card_declined"
	// KindGatewayRejected: the gateway understood the request and refused it
	// (bad credentials, refund of an unsettled charge, duplicate). No money moved.
	KindGatewayRejected Kind = "gateway_rejected"
	// KindGatewayUnavailable: the gateway could not be reached at all. The
	// request was never delivered, so retrying is safe.
	KindGatewayUnavailable Kind = "gateway_unavailable"
	// KindOutcomeUnknown: the request may have been processed, but no usable
	// answer came back. Retrying could charge the card twice; reconcile first.
	KindOutcomeUnknown Kind = "outcome_unknown"
)

// Error is the single error type that crosses the Gateway boundary.
type Error struct {
	Kind    Kind
	Code    string // gateway's own code, when there is one
	Message string // safe to show to an API caller; never contains card data
	Field   string // offending request field, for KindInvalidRequest
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s (code %s)", e.Kind, e.Message, e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

// Invalid returns a KindInvalidRequest error for a request field.
func Invalid(field, message string) *Error {
	return &Error{Kind: KindInvalidRequest, Field: field, Message: message}
}

// Registry holds the gateways enabled for this process.
type Registry struct {
	gateways map[string]Gateway
}

// NewRegistry builds a registry. Names are matched case-insensitively.
func NewRegistry(gateways ...Gateway) *Registry {
	r := &Registry{gateways: make(map[string]Gateway, len(gateways))}
	for _, g := range gateways {
		r.gateways[strings.ToLower(g.Name())] = g
	}
	return r
}

// Get returns the named gateway, or an invalid-request error that lists the
// available names.
func (r *Registry) Get(name string) (Gateway, error) {
	if g, ok := r.gateways[strings.ToLower(name)]; ok {
		return g, nil
	}
	if name == "" {
		return nil, Invalid("gateway", "gateway is required; available: "+strings.Join(r.Names(), ", "))
	}
	return nil, Invalid("gateway", "unknown gateway; available: "+strings.Join(r.Names(), ", "))
}

// Names returns the enabled gateway names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.gateways))
	for name := range r.gateways {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// FormatMinor renders an amount in minor units as a two-decimal string, the
// form most card gateways expect ("1050" becomes "10.50"). Integer
// arithmetic only: money never passes through a float.
func FormatMinor(amount int64) string {
	return fmt.Sprintf("%d.%02d", amount/100, amount%100)
}
