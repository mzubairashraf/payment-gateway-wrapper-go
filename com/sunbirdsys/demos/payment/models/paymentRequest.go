package models

type PaymentRequest struct {
	GatewayName string
	CreditCard  CreditCard
	Amount      string
	Description string
}
