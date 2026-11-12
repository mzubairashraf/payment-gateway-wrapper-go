package payment

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// Card is a payment card as supplied by the caller.
//
// Card data is the most sensitive value in the system. The type redacts
// itself under every formatting verb and in structured logs, so a stray
// log.Printf("%v", request) cannot leak a card number or CVV.
type Card struct {
	Number   string
	ExpMonth int
	ExpYear  int // four digits
	CVV      string
}

// Last4 returns the last four digits, the only part safe to store or show.
func (c Card) Last4() string {
	if len(c.Number) < 4 {
		return ""
	}
	return c.Number[len(c.Number)-4:]
}

// Expiry returns the expiry as YYYY-MM.
func (c Card) Expiry() string { return fmt.Sprintf("%04d-%02d", c.ExpYear, c.ExpMonth) }

func (c Card) String() string       { return "card(****" + c.Last4() + ")" }
func (c Card) GoString() string     { return c.String() }
func (c Card) LogValue() slog.Value { return slog.StringValue(c.String()) }

// MarshalJSON redacts too: loggers serialise nested structs with
// encoding/json, which ignores String and LogValue. Gateways that need the
// real values read the fields directly.
func (c Card) MarshalJSON() ([]byte, error) { return json.Marshal(c.String()) }

// Validate checks the card's shape before it is sent anywhere. It cannot
// tell whether the card is real; it only stops requests that no gateway
// would accept.
func (c Card) Validate(now time.Time) *Error {
	if n := len(c.Number); n < 12 || n > 19 || !allDigits(c.Number) {
		return Invalid("card.number", "card number must be 12 to 19 digits")
	}
	if !luhnValid(c.Number) {
		return Invalid("card.number", "card number failed the checksum; check for a typo")
	}
	if c.ExpMonth < 1 || c.ExpMonth > 12 {
		return Invalid("card.exp_month", "exp_month must be 1 to 12")
	}
	if c.ExpYear < 2000 || c.ExpYear > 2100 {
		return Invalid("card.exp_year", "exp_year must be a four-digit year")
	}
	// A card is valid through the last day of its expiry month.
	y, m := now.UTC().Year(), int(now.UTC().Month())
	if c.ExpYear < y || (c.ExpYear == y && c.ExpMonth < m) {
		return Invalid("card.exp_year", "card has expired")
	}
	if n := len(c.CVV); n < 3 || n > 4 || !allDigits(c.CVV) {
		return Invalid("card.cvv", "cvv must be 3 or 4 digits")
	}
	return nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// luhnValid implements the Luhn checksum used by all major card schemes.
func luhnValid(number string) bool {
	sum, double := 0, false
	for i := len(number) - 1; i >= 0; i-- {
		d := int(number[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}
