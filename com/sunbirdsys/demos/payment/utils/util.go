package utils

import (
	"encoding/json"
	"math/rand"
	"net/http"
)

func Message(status bool, message string) map[string]interface{} {
	return map[string]interface{}{"status": status, "message": message}
}

func Respond(w http.ResponseWriter, data map[string]interface{}) {
	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func RangeIn(low, hi int) int {
	return low + rand.Intn(hi-low)
}
