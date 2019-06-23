package comm

import (
	"bytes"
	"com/sunbirdsys/demos/payment/models"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HttpHandler struct {
	Username   string
	Password   string
	APIKey     string
	Endpoint   string
	HTTPClient *http.Client // The HTTP client to send requests on
}

type contentType string

const (
	contentTypeEmpty          contentType = ""
	contentTypeJSON           contentType = "application/json"
	contentTypeXML            contentType = "application/xml"
	contentTypeFormURLEncoded contentType = "application/x-www-form-urlencoded"
	httpClientTimeout                     = 15 * time.Second
)

// New creates a new client object.
func New(username string, password string, apiKey string, endpoint string) *HttpHandler {
	return &HttpHandler{
		Username: username,
		Password: password,
		APIKey:   apiKey,
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Timeout: httpClientTimeout,
		},
	}
}

func (c *HttpHandler) Request(v interface{}, method, path string, data interface{}, queryString url.Values) error {
	if !strings.HasPrefix(path, "https://") && !strings.HasPrefix(path, "http://") {
		path = fmt.Sprintf("%s/%s", c.Endpoint, path)
	}
	uri, err := url.Parse(path)
	if err != nil {
		return err
	}

	body, contentType, err := prepareRequestBody(data)
	if err != nil {
		return err
	}

	request, err := http.NewRequest(method, uri.String(), bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	request.Header.Set("Accept", "application/json")

	log.Printf("## Using Username %v", c.Username)
	if c.Username != "" {
		request.SetBasicAuth(c.Username, c.Password)
	}

	if contentType != contentTypeEmpty {
		request.Header.Set("Content-Type", string(contentType))
	}

	if c.APIKey != "" {
		request.Header.Set("key", c.APIKey)
	}

	if queryString != nil {
		request.URL.RawQuery = queryString.Encode()
	}

	if data != nil {
		log.Printf("HTTP REQUEST: %s %s %s", method, uri.String(), body)
	} else {
		log.Printf("HTTP REQUEST: %s %s", method, uri.String())
	}

	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return err
	}

	defer response.Body.Close()

	responseBody, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}
	responseBody = bytes.TrimPrefix(responseBody, []byte("\xef\xbb\xbf"))
	log.Printf("HTTP RESPONSE: %s", string(responseBody))
	log.Printf("HTTP RESPONSE Code : %d", response.StatusCode)

	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated:
		// Status codes 200 and 201 are indicative of being able to convert the
		// response body to the struct that was specified.
		if err := json.Unmarshal(responseBody, &v); err != nil {
			return fmt.Errorf("could not decode response JSON, %s: %v", string(responseBody), err)
		}

		return nil
	case http.StatusNoContent:
		// Status code 204 is returned for successful DELETE requests. Don't try to
		// unmarshal the body: that would return errors.
		return nil
	case http.StatusInternalServerError:
		// Status code 500 is a server error and means nothing can be done at this
		// point.
		return errors.New("## Received Server Error 500 ##")
	default:
		// Anything else than a 200/201/204/500 should be a JSON error.
		var errorResponse models.ErrorResponse
		if err := json.Unmarshal(responseBody, &errorResponse.InvalidJSONMap); err != nil {
			return err
		}
		return errorResponse
	}
}

// prepareRequestBody takes untyped data and attempts constructing a meaningful
// request body from it. It also returns the appropriate Content-Type.
func prepareRequestBody(data interface{}) ([]byte, contentType, error) {
	switch data := data.(type) {
	case nil:
		// Nil bodies are accepted by `net/http`, so this is not an error.
		return nil, contentTypeEmpty, nil
	case string:
		return []byte(data), contentTypeFormURLEncoded, nil
	default:
		b, err := json.Marshal(data)
		if err != nil {
			return nil, contentType(""), err
		}

		return b, contentTypeJSON, nil
	}
}
