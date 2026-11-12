// Package simulator is an in-memory payment.Gateway with deterministic
// behaviour, selected by the card number. It lets the service be run and
// demonstrated without merchant credentials, and gives the HTTP tests a
// gateway whose every outcome can be triggered on demand.
package simulator

import (
	"context"
	"fmt"
	"sync"

	"github.com/mzubairashraf/payment-gateway-wrapper-go/internal/payment"
)

// Test cards. Any other valid card number is approved.
const (
	CardApproved = "4111111111111111"
	CardDeclined = "4000000000000002"
	CardHeld     = "4000000000000044" // accepted, held for review
	CardUnknown  = "4000000000000069" // simulates a lost response
	CardDown     = "4000000000000119" // simulates an unreachable gateway
)

type charge struct {
	amount, refunded int64
	currency, last4  string
}

// Gateway is the simulator. It is safe for concurrent use.
type Gateway struct {
	mu      sync.Mutex
	charges map[string]*charge
	next    int
}

func New() *Gateway { return &Gateway{charges: map[string]*charge{}} }

func (g *Gateway) Name() string { return "simulator" }

func (g *Gateway) Charge(_ context.Context, req payment.ChargeRequest) (*payment.Charge, error) {
	status := payment.StatusApproved
	switch req.Card.Number {
	case CardDeclined:
		return nil, &payment.Error{Kind: payment.KindDeclined, Code: "insufficient_funds", Message: "the card was declined: insufficient funds"}
	case CardUnknown:
		return nil, &payment.Error{Kind: payment.KindOutcomeUnknown, Message: "the gateway did not answer in time"}
	case CardDown:
		return nil, &payment.Error{Kind: payment.KindGatewayUnavailable, Message: "could not connect to the gateway"}
	case CardHeld:
		status = payment.StatusHeldForReview
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	id := fmt.Sprintf("sim_ch_%06d", g.next)
	g.charges[id] = &charge{amount: req.Amount, currency: req.Currency, last4: req.Card.Last4()}
	return &payment.Charge{
		TransactionID: id, Status: status, AuthCode: fmt.Sprintf("A%05d", g.next),
		CardBrand: brand(req.Card.Number), CardLast4: req.Card.Last4(),
		Amount: req.Amount, Currency: req.Currency,
	}, nil
}

func (g *Gateway) Refund(_ context.Context, req payment.RefundRequest) (*payment.Refund, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.charges[req.TransactionID]
	switch {
	case !ok:
		return nil, &payment.Error{Kind: payment.KindGatewayRejected, Code: "transaction_not_found", Message: "no charge with that transaction_id"}
	case req.Currency != c.currency:
		return nil, &payment.Error{Kind: payment.KindGatewayRejected, Code: "currency_mismatch", Message: "refund currency differs from the charge"}
	case req.Amount > c.amount-c.refunded:
		return nil, &payment.Error{Kind: payment.KindGatewayRejected, Code: "amount_too_large", Message: "refund exceeds the unrefunded amount of the charge"}
	}
	c.refunded += req.Amount
	g.next++
	return &payment.Refund{TransactionID: fmt.Sprintf("sim_re_%06d", g.next), Amount: req.Amount, Currency: req.Currency}, nil
}

func brand(number string) string {
	switch number[0] {
	case '4':
		return "Visa"
	case '5', '2':
		return "Mastercard"
	case '3':
		return "American Express"
	default:
		return "Card"
	}
}
