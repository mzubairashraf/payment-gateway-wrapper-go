package authorizedotnet

type authorizeDotNetRequest struct {
	CreateTransactionRequest struct {
		MerchantAuthentication struct {
			Name           string `json:"name"`
			TransactionKey string `json:"transactionKey"`
		} `json:"merchantAuthentication"`
		RefID              string `json:"refId"`
		TransactionRequest struct {
			TransactionType string `json:"transactionType"`
			Amount          string `json:"amount"`
			Payment         struct {
				CreditCard struct {
					CardNumber     string `json:"cardNumber"`
					ExpirationDate string `json:"expirationDate"`
					CardCode       string `json:"cardCode"`
				} `json:"creditCard"`
			} `json:"payment"`
		} `json:"transactionRequest"`
	} `json:"createTransactionRequest"`
}
