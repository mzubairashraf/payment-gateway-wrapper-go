package controllers

import (
	"com/sunbirdsys/demos/payment/models"
	"com/sunbirdsys/demos/payment/pg"
	"com/sunbirdsys/demos/payment/utils"
	"encoding/json"
	"log"
	"net/http"
)

func ChargeCard(w http.ResponseWriter, r *http.Request) {

	payRequest := &models.PaymentRequest{}

	err := json.NewDecoder(r.Body).Decode(payRequest)
	if err != nil {
		log.Print(err)
		utils.Respond(w, utils.Message(false, "Invalid request"))
		return
	}

	log.Printf("Payment Request %v: ", payRequest)

	gatewayHandler := pg.GetGatewayHandler(payRequest.GatewayName)
	payResp, err := gatewayHandler.ChargeCard(*payRequest)

	if err != nil {
		log.Print(err)

		utils.Respond(w, utils.Message(false, "Unable to charge card"))
		return
	}

	resp := utils.Message(true, "Card charged successfully")
	resp["PaymentResponse"] = payResp
	utils.Respond(w, resp)
}

func RefundCard(w http.ResponseWriter, r *http.Request) {

	refundRequest := &models.RefundRequest{}

	err := json.NewDecoder(r.Body).Decode(refundRequest)
	if err != nil {
		log.Print(err)
		utils.Respond(w, utils.Message(false, "Invalid request"))
		return
	}

	log.Printf("Refund Request %v: ", refundRequest)

	gatewayHandler := pg.GetGatewayHandler(refundRequest.GatewayName)
	refundResp, err := gatewayHandler.RefundCard(*refundRequest)

	if err != nil {
		log.Print(err)

		utils.Respond(w, utils.Message(false, "Unable to refund"))
		return
	}

	resp := utils.Message(true, "Refund trans processed successfully")
	resp["RefundResponse"] = refundResp
	utils.Respond(w, resp)
}
