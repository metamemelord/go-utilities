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

func New(logger *log.Logger) *httpRequest {
	request, err := http.NewRequest("", "", nil)
	if err != nil {
		if logger != nil {
			logger.Error("[ERROR] Failed to create a http request")
		}
		return nil
	}
	return &httpRequest{
		request: request,
		header:  make(map[string]string),
		retries: 4, // 1 + 3 retries. Leaving for now, will need to be properly piped into a worker with retires treated as a seperate action
		timeout: 30 * time.Second,
		logger:  logger,
	}
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
	h.reqest.Body = reader
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
	if h.request.URL.String() == constants.EmptyString {
		return nil, fmt.Errorf("Request URI must be specified")
	}

	retries := h.retries
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

	for retries != 0 {
		retries--

		response, err := client.Do(h.request)
		if err != nil {
			if urlError, ok := err.(*url.Error); ok {
				if urlError.Timeout() {
					if h.logger != nil {
						h.logger.WithFields(getRequestFields(h.request.Method,
							h.request.URL.RequestURI(),
							string(h.payload),
							h.header,
							fmt.Errorf("Call failed at retry number %d", h.retries-retries-1))).
							Errorln("Request timed out")
					}
					continue
				}
			} else {
				if h.logger != nil {
					err = multierr.Append(err, fmt.Errorf("Call failed at retry number %d", h.retries-retries-1))
					h.logger.WithFields(getRequestFields(h.request.Method,
						h.request.URL.RequestURI(),
						string(h.payload),
						h.header,
						err)).
						Errorf("API call failed")
				}
			}
			continue
		}

		responsePayload, err := ioutil.ReadAll(response.Body)

		if err != nil {
			return nil, err
		}

		responseBodyReader := bytes.NewReader(responsePayload)
		response.Body = byteReaderCloser{responseBodyReader}

		if h.logger != nil {
			logFieldMap := getRequestFiel(h.request.Method,
				h.request.URL.RequestURI(),
				string(h.payload),
				h.header,
				nil)
			logFieldMap["http.response.payload"] = string(responsePayload)
			logFieldMap["http.response.code"] = response.StatusCode
			h.logger.WithFields(logFieldMap).
				Infof("API call successful")
		}
		return response, nil
	}

	if h.logger != nil {
		h.logger.WithFields(getRequestFields(h.request.Method,
			h.request.URL.RequestURI(),
			string(h.payload),
			h.header,
			fmt.Errorf("Calls failed after %d reties", h.retries))).
			Errorf("API call failed")
	}
	return nil, fmt.Errorf("Request failed")
}

func getRequestFields(method, uri, payload string, headers map[string]string, err error) logger.Fields {
	fields := logger.Fields{}
	if method != "" {
		fields["http.request.method"] = method
	}
	if uri != "" {
		fields["http.request.uri"] = uri
	}
	if payload != "" {
		fields["http.request.payload"] = payload
	}
	if headers != nil {
		fields["http.request.headers"] = headers
	}
	if err != nil {
		fields["error"] = err
	}
	return fields
}
