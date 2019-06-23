package main

import (
	"com/sunbirdsys/demos/payment/config"
	"com/sunbirdsys/demos/payment/controllers"
	"github.com/gorilla/mux"
	"log"
	"net/http"
)

func main() {

	appConfig, err := config.InitConfig()
	if err != nil {
		log.Panicln("## Error while reading config file", err)
		return
	}

	router := mux.NewRouter()
	router.HandleFunc("/_api/card/charge", controllers.ChargeCard).Methods("POST")
	router.HandleFunc("/_api/card/refund", controllers.RefundCard).Methods("POST")

	log.Println("##  Starting Server on Port ", appConfig.PORT)

	err = http.ListenAndServe(":"+appConfig.PORT, router)
	if err != nil {
		log.Fatal("Error while initializing server", err)
	}
}
