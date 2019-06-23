package vantiv

type vantivRequest struct {
	Amount int `json:"amount"`
	Card   struct {
		Number         string `json:"number"`
		Cvv            string `json:"cvv"`
		ExpirationDate string `json:"expirationDate"`
		Address        struct {
			Line1 string `json:"line1"`
			City  string `json:"city"`
			State string `json:"state"`
			Zip   string `json:"zip"`
		} `json:"address"`
	} `json:"card"`
	ExtendedInformation struct {
		TypeOfGoods string `json:"typeOfGoods"`
	} `json:"extendedInformation"`
	DeveloperApplication struct {
		DeveloperID int    `json:"developerId"`
		Version     string `json:"Version"`
	} `json:"developerApplication"`
}
