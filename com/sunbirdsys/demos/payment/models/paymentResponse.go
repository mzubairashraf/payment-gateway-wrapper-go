package models

type PaymentResponse struct {
	Amount              *string `json:",omitempty"`
	CardType            *string `json:",omitempty"`
	AccountNumber       *string `json:",omitempty"`
	GatewayResponseCode *string `json:",omitempty"`
	GatewayTransId      *string `json:",omitempty"`
	TransId             *string `json:",omitempty"`
}
