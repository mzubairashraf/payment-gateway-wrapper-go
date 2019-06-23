package models

const (
	apiErrMessage = "Payment Gateway API returned an error"
)

// Error holds details including error code, human readable description and optional parameter that is related to the error.
type Error struct {
	Code        int
	Description string
	Parameter   string
}

// Error implements error interface.
func (e Error) Error() string {
	return e.Description
}

// ErrorResponse represents errored API response.
type ErrorResponse struct {
	Errors         []Error `json:"errors"`
	InvalidJSONMap map[string]interface{}
}

// Error implements error interface.
func (r ErrorResponse) Error() string {
	return apiErrMessage
}
