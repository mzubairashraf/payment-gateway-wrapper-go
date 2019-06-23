package vantiv

import "com/sunbirdsys/demos/payment/models"

type VantivHandler struct {
}

func (x VantivHandler) ChargeCard(request models.PaymentRequest) (*models.PaymentResponse, error) {
	return nil, nil
}

func (x VantivHandler) RefundCard(request models.RefundRequest) (*models.RefundResponse, error) {
	return nil, nil
}
