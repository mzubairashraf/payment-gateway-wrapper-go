package authorizedotnet

import (
	"com/sunbirdsys/demos/payment/comm"
	"com/sunbirdsys/demos/payment/models"
	"com/sunbirdsys/demos/payment/utils"
	"fmt"
	"github.com/spf13/viper"
	"log"
	"net/http"
	"strconv"
)

type AuthorizeDotNetHandler struct {
}

func (x AuthorizeDotNetHandler) ChargeCard(request models.PaymentRequest) (*models.PaymentResponse, error) {
	authDotNetChargeReq, err := prepareAuthorizeDotNetChargeRequest(request)
	if err != nil {
		return nil, err
	}

	endpoint := viper.GetString("AuthorizeDotNetConfig.Endpoint")

	httpHandler := comm.New("", "", "", endpoint)

	authDotNetChargeResp := &authorizeDotNetResponse{}

	log.Printf("## Sending charge request %v: ", authDotNetChargeReq)
	err = httpHandler.Request(authDotNetChargeResp, http.MethodPost, "", authDotNetChargeReq, nil)

	if err != nil {
		return nil, err
	}

	payResp, err := parseAuthorizeDotNetChargeResponse(*authDotNetChargeResp)

	log.Printf("## Charge response %v: ", payResp)
	return payResp, err
}

func (x AuthorizeDotNetHandler) RefundCard(request models.RefundRequest) (*models.RefundResponse, error) {
	return nil, nil
}

func prepareAuthorizeDotNetChargeRequest(payRequest models.PaymentRequest) (*authorizeDotNetRequest, error) {
	authDotNetReq := &authorizeDotNetRequest{}
	authDotNetReq.CreateTransactionRequest.MerchantAuthentication.Name = viper.GetString("AuthorizeDotNetConfig.APILoginId")
	authDotNetReq.CreateTransactionRequest.MerchantAuthentication.TransactionKey = viper.GetString("AuthorizeDotNetConfig.TransactionKey")
	authDotNetReq.CreateTransactionRequest.TransactionRequest.Amount = payRequest.Amount
	authDotNetReq.CreateTransactionRequest.TransactionRequest.TransactionType = "authCaptureTransaction"
	authDotNetReq.CreateTransactionRequest.TransactionRequest.Payment.CreditCard.CardNumber = payRequest.CreditCard.CardNo
	authDotNetReq.CreateTransactionRequest.TransactionRequest.Payment.CreditCard.CardCode = payRequest.CreditCard.CardCvn
	authDotNetReq.CreateTransactionRequest.TransactionRequest.Payment.CreditCard.ExpirationDate = payRequest.CreditCard.CardExp

	rand := utils.RangeIn(00000000000, 9999999999999)
	authDotNetReq.CreateTransactionRequest.RefID = strconv.Itoa(rand)
	return authDotNetReq, nil
}

func parseAuthorizeDotNetChargeResponse(authDotNetResp authorizeDotNetResponse) (*models.PaymentResponse, error) {
	payResponse := &models.PaymentResponse{}

	if authDotNetResp.Messages.ResultCode == "Ok" {
		payResponse.GatewayResponseCode = &authDotNetResp.TransactionResponse.ResponseCode
		payResponse.GatewayTransId = &authDotNetResp.TransactionResponse.TransID
		payResponse.TransId = &authDotNetResp.TransactionResponse.RefTransID
	} else {
		return payResponse, fmt.Errorf("transaction failed")
	}

	return payResponse, nil
}
