package authorizedotnet

type authorizeDotNetResponse struct {
	TransactionResponse struct {
		ResponseCode   string `json:"responseCode"`
		AuthCode       string `json:"authCode"`
		AvsResultCode  string `json:"avsResultCode"`
		CvvResultCode  string `json:"cvvResultCode"`
		CavvResultCode string `json:"cavvResultCode"`
		TransID        string `json:"transId"`
		RefTransID     string `json:"refTransID"`
		TransHash      string `json:"transHash"`
		TestRequest    string `json:"testRequest"`
		AccountNumber  string `json:"accountNumber"`
		AccountType    string `json:"accountType"`
		Messages       []struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"messages"`
		TransHashSha2                          string `json:"transHashSha2"`
		SupplementalDataQualificationIndicator int    `json:"SupplementalDataQualificationIndicator"`
	} `json:"transactionResponse"`
	Messages struct {
		ResultCode string `json:"resultCode"`
		Message    []struct {
			Code string `json:"code"`
			Text string `json:"text"`
		} `json:"message"`
	} `json:"messages"`
}
