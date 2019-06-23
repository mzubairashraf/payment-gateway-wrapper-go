package models

type RefundRequest struct {
	GatewayName     string
	GatewayRefundId string
	Amount          string
}
