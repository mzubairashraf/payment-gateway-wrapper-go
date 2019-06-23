package models

type RefundResponse struct {
	RefundStatus   *string `json:",omitempty"`
	RefundAmount   *string `json:",omitempty"`
	GatewayTransId *string `json:",omitempty"`
	TransID        *string `json:",omitempty"`
}
