package pg

import (
	"com/sunbirdsys/demos/payment/models"
	"com/sunbirdsys/demos/payment/pg/authorizedotnet"
	"com/sunbirdsys/demos/payment/pg/vantiv"
	"log"
)

type GatewayHandler interface {
	ChargeCard(request models.PaymentRequest) (*models.PaymentResponse, error)
	RefundCard(request models.RefundRequest) (*models.RefundResponse, error)
}

func GetGatewayHandler(gatewayName string) GatewayHandler {

	switch gatewayName {
	case "Vantiv":
		return &vantiv.VantivHandler{}
	case "AuthorizeDotNet":
		return &authorizedotnet.AuthorizeDotNetHandler{}
	default:
		log.Printf("## Invalid gateway name ##")
		return nil
	}
}
