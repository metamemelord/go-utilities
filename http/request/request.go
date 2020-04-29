package request

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"time"

	"go.uber.org/multierr"
)

type httpRequest struct {
	request *http.Request
	timeout time.Duration
	payload []byte
	header  map[string]string
	retries uint8
	logger  *log.Logger
}

type byteReaderCloser struct {
	io.Reader
}

func (byteReaderCloser) Close() error { return nil }

func New(logger *log.Logger) (*httpRequest, error) {
	request, err := http.NewRequest("", "", nil)

	if err != nil {
		return nil, err
	}

	return &httpRequest{
		request: request,
		header:  make(map[string]string),
		retries: 0,
		timeout: 30 * time.Second,
		logger:  logger,
	}, nil
}

func (h *httpRequest) SetContext(ctx context.Context) *httpRequest {
	h.request = h.request.WithContext(ctx)
	return h
}

func (h *httpRequest) SetMethod(method string) *httpRequest {
	if method != "GET" &&
		method != "POST" &&
		method != "PUT" &&
		method != "DELETE" {
		if h.logger != nil {
			h.logger.Printf("[ERROR] Invalid/Unsupported http method: %s", method)
		}
		return nil
	}
	h.request.Method = method
	return h
}

func (h *httpRequest) SetURI(uri string) *httpRequest {
	u, err := url.Parse(uri)
	if err != nil {
		h.logger.Printf("[ERROR] Invalid URL %s", uri)
		return nil
	}
	h.request.URL = u
	return h
}

func (h *httpRequest) SetPayloadFromReader(reader io.ReadCloser) *httpRequest {
	h.request.Body = reader
	return h
}

func (h *httpRequest) SetPayload(payload []byte) *httpRequest {
	h.request.Body = byteReaderCloser{bytes.NewBuffer(payload)}
	h.payload = payload
	return h
}

func (h *httpRequest) SetHeader(key, value string) *httpRequest {
	h.request.Header.Set(key, value)
	h.header[key] = value
	return h
}

func (h *httpRequest) SetCookie(requestCookie *http.Cookie) *httpRequest {
	h.request.AddCookie(requestCookie)
	h.header["Cookie"] = requestCookie.String()
	return h
}

func (h *httpRequest) SetTimeout(timeout time.Duration) *httpRequest {
	h.timeout = timeout * time.Second
	return h
}

func (h *httpRequest) SetRetries(retries uint8) *httpRequest {
	h.retries = retries + 1
	return h
}

func (h *httpRequest) Do() (*http.Response, error) {
	if h.request.URL.String() == "" {
		return nil, fmt.Errorf("Request URI must be specified")
	}

	client := &http.Client{Timeout: h.timeout}

	if (h.payload == nil || len(h.payload) == 0) && h.request.Body != nil {
		requestPayload, err := ioutil.ReadAll(h.request.Body)
		requestBodyReader := bytes.NewReader(requestPayload)
		h.request.Body = byteReaderCloser{requestBodyReader}
		if err != nil {
			return nil, err
		}
		h.payload = requestPayload
	}

	response, err := client.Do(h.request)
	if h.retries == 0 {
		return response, err
	}

	var retries uint8 = 1
	log.Println("[INFO]: Starting retries...")
	for retries <= h.retries {
		response, err = client.Do(h.request)
		if err != nil {
			if urlError, ok := err.(*url.Error); ok {
				if urlError.Timeout() {
					log.Println("[ERROR]: Request timed out")
				}
			} else {
				err = multierr.Append(err, fmt.Errorf("Call failed at retry number %d", retries))
				log.Println("[ERROR]:", err)
			}
			retries++
			continue
		}

		responsePayload, err := ioutil.ReadAll(response.Body)

		if err != nil {
			return nil, err
		}

		responseBodyReader := bytes.NewReader(responsePayload)
		response.Body = byteReaderCloser{responseBodyReader}

		return response, nil
	}

	return nil, fmt.Errorf("Request failed")
}
