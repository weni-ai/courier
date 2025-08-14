package utils

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RequestResponseStatus represents the status of a WebhookRequeset
type RequestResponseStatus string

// RequestResponse represents both the outgoing request and response for a particular URL/method/body
type RequestResponse struct {
	Method        string
	URL           string
	Status        RequestResponseStatus
	StatusCode    int
	Request       string
	Response      string
	Body          []byte
	ContentLength int
	Elapsed       time.Duration
}

const (
	// RRStatusSuccess represents that the webhook was successful
	RRStatusSuccess RequestResponseStatus = "S"

	// RRConnectionFailure represents that the webhook had a connection failure
	RRConnectionFailure RequestResponseStatus = "F"

	// RRStatusFailure represents that the webhook had a non 2xx status code
	RRStatusFailure RequestResponseStatus = "E"
)

// MakeInsecureHTTPRequest fires the passed in http request against a transport that does not validate
// SSL certificates.
func MakeInsecureHTTPRequest(req *http.Request) (*RequestResponse, error) {
	return MakeHTTPRequestWithClient(req, GetInsecureHTTPClient())
}

// MakeHTTPRequest fires the passed in http request, returning any errors encountered. RequestResponse is always set
// regardless of any errors being set
func MakeHTTPRequest(req *http.Request) (*RequestResponse, error) {
	return MakeHTTPRequestWithClient(req, GetHTTPClient())
}

// MakeHTTPRequestWithClient makes an HTTP request with the passed in client, returning a
// RequestResponse containing logging information gathered during the request
func MakeHTTPRequestWithClient(req *http.Request, client *http.Client) (*RequestResponse, error) {
	req.Header.Set("User-Agent", HTTPUserAgent)

	start := time.Now()
	requestTrace, err := httputil.DumpRequestOut(req, true)
	if err != nil {
		rr, _ := newRRFromRequestAndError(req, string(requestTrace), err)
		return rr, err
	}

	resp, err := client.Do(req)
	if err != nil {
		rr, _ := newRRFromRequestAndError(req, string(requestTrace), err)
		return rr, err
	}
	defer resp.Body.Close()

	rr, err := newRRFromResponse(req.Method, string(requestTrace), resp)
	rr.Elapsed = time.Since(start)
	return rr, err
}

// newRRFromResponse creates a new RequestResponse based on the passed in http request and error (when we received no response)
func newRRFromRequestAndError(r *http.Request, requestTrace string, requestError error) (*RequestResponse, error) {
	rr := RequestResponse{ContentLength: -1}
	rr.Method = r.Method
	rr.URL = r.URL.String()

	rr.Request = requestTrace
	rr.Status = RRConnectionFailure
	rr.Body = []byte(requestError.Error())

	return &rr, nil
}

// newRRFromResponse creates a new RequestResponse based on the passed in http Response
func newRRFromResponse(method string, requestTrace string, r *http.Response) (*RequestResponse, error) {
	var err error
	rr := RequestResponse{ContentLength: -1}
	rr.Method = method
	rr.URL = r.Request.URL.String()
	rr.StatusCode = r.StatusCode

	// set our content length if we have its header

	if r.Header.Get("Content-Length") != "" {
		contentLength, err := strconv.Atoi(r.Header.Get("Content-Length"))
		if err == nil {
			rr.ContentLength = contentLength
		}
	}

	// set our status based on our status code
	if rr.StatusCode/100 == 2 {
		rr.Status = RRStatusSuccess
	} else {
		rr.Status = RRStatusFailure
	}

	rr.Request = requestTrace

	// figure out if our Response is something that looks like text from our headers
	isText := false
	contentType := r.Header.Get("Content-Type")
	if contentType == "" ||
		strings.Contains(contentType, "text") ||
		strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "javascript") ||
		strings.Contains(contentType, "urlencoded") ||
		strings.Contains(contentType, "utf") ||
		strings.Contains(contentType, "xml") {

		isText = true
	}

	// only dump the whole body if this looks like text
	response, err := httputil.DumpResponse(r, isText)
	if err != nil {
		return &rr, err
	}

	rr.Response = string(response)

	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return &rr, err
	}
	rr.Body = bodyBytes

	// return an error if we got a non-200 status
	if rr.Status != RRStatusSuccess {
		err = fmt.Errorf("received non 200 status: %d", rr.StatusCode)
	}

	return &rr, err
}

// GetHTTPClient returns the shared HTTP client used by all Courier threads
func GetHTTPClient() *http.Client {
	once.Do(func() {
		transport = http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConns = 64
		transport.MaxIdleConnsPerHost = 8
		transport.IdleConnTimeout = 15 * time.Second
		client = &http.Client{
			Transport: transport,
			Timeout:   60 * time.Second,
		}
	})

	return client
}

// GetInsecureHTTPClient returns the shared HTTP client used by all Courier threads
func GetInsecureHTTPClient() *http.Client {
	insecureOnce.Do(func() {
		insecureTransport = http.DefaultTransport.(*http.Transport).Clone()
		insecureTransport.MaxIdleConns = 64
		insecureTransport.MaxIdleConnsPerHost = 8
		insecureTransport.IdleConnTimeout = 15 * time.Second
		insecureTransport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		insecureClient = &http.Client{
			Transport: insecureTransport,
			Timeout:   60 * time.Second,
		}
	})

	return insecureClient
}

var (
	transport *http.Transport
	client    *http.Client
	once      sync.Once

	insecureTransport *http.Transport
	insecureClient    *http.Client
	insecureOnce      sync.Once

	HTTPUserAgent = "Courier/vDev"
)

// MakeHTTPRequestWithRetry makes an HTTP request with the passed in client, returning a
// RequestResponse containing logging information gathered during the request
func MakeHTTPRequestWithRetry(ctx context.Context, original *http.Request, attempts int, baseBackoff time.Duration, idempotencyKey string) (*RequestResponse, error) {
	if attempts < 1 {
		attempts = 1
	}
	// snapshot the body to be able to recreate the request
	var bodyBytes []byte
	if original.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(original.Body)
		_ = original.Body.Close()
		if err != nil {
			return nil, err
		}
	}

	client := GetHTTPClient()
	var lastRR *RequestResponse
	var lastErr error

	for i := 0; i < attempts; i++ {
		req, err := http.NewRequestWithContext(ctx, original.Method, original.URL.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header = original.Header.Clone()
		if idempotencyKey != "" && (original.Method == http.MethodPost || original.Method == http.MethodPut || original.Method == http.MethodPatch) {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		// avoid reuse of connection in LBs/Cloudflare in problematic cases
		req.Close = true

		rr, err := MakeHTTPRequestWithClient(req, client)
		lastRR, lastErr = rr, err

		if err == nil && rr != nil && rr.Status == RRStatusSuccess {
			return rr, nil
		}
		if !shouldRetryTransient(rr, err) || i == attempts-1 {
			break
		}
		time.Sleep(withJitter(baseBackoff, i))
	}
	return lastRR, lastErr
}

func shouldRetryTransient(rr *RequestResponse, err error) bool {
	// Always honor retriable HTTP status codes if a response exists
	if rr != nil {
		if rr.StatusCode == http.StatusTooManyRequests ||
			rr.StatusCode == http.StatusBadGateway ||
			rr.StatusCode == http.StatusServiceUnavailable ||
			rr.StatusCode == http.StatusGatewayTimeout {
			return true
		}
	}

	if err != nil {
		var netErr net.Error
		if errors.Is(err, io.EOF) ||
			errors.Is(err, syscall.ECONNRESET) ||
			errors.Is(err, context.DeadlineExceeded) ||
			errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
			return true
		}
		var _tlsErr *tls.RecordHeaderError
		if errors.As(err, &_tlsErr) {
			return true
		}
	}
	return false
}

func withJitter(base time.Duration, attempt int) time.Duration {
	backoff := base << attempt
	j := time.Duration(rand.Int63n(int64(backoff / 2)))
	return backoff/2 + j
}
