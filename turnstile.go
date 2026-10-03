// Package turnstile solves Cloudflare Turnstile widgets with the ZeroCaptcha REST API: give it a
// page's URL and its sitekey, and it returns a token to submit as the browser would. It uses the
// standard library only.
package turnstile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Task is what to solve: the page the widget is on and its sitekey, as the widget declares them.
type Task struct {
	WebsiteURL string
	WebsiteKey string
	// Action is the widget's data-action, if it sets one.
	Action string
	// CData is the widget's data-cdata, if it sets one.
	CData string
	// Proxy is your proxy, such as http://user:pass@proxy.example.net:8080, to solve through it.
	Proxy string
}

// Client calls the ZeroCaptcha API with one account's key.
type Client struct {
	// API is the API's address, such as https://api.zerocaptcha.io.
	API string
	// Key is your API key, zc_live_….
	Key string
	// Interval is how often to ask for the result: 2 seconds when zero.
	Interval time.Duration
	// HTTP is the client to use: http.DefaultClient when nil.
	HTTP *http.Client
}

// Error is a refusal from the API, a task that ended without a token, or a wait that ran out.
type Error struct {
	// Code is the API's code, such as insufficient_funds or ERROR_CAPTCHA_UNSOLVABLE, or timeout.
	Code    string
	Message string
	// RequestID is what to quote when you ask support about the request.
	RequestID string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type task struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Solution *struct {
		Token string `json:"token"`
	} `json:"solution"`
	ErrorCode        string `json:"errorCode"`
	ErrorDescription string `json:"errorDescription"`
}

const attempts = 3

// Solve creates a Cloudflare Turnstile task, waits for it, and returns the token. The token works
// once, for 300 seconds. A task that fails or expires is an *Error with its errorCode, and nothing
// is charged. Give ctx a deadline, such as three minutes, to bound the wait.
func (c *Client) Solve(ctx context.Context, t Task) (string, error) {
	body := map[string]string{
		"type":       "TurnstileTaskProxyless",
		"websiteURL": t.WebsiteURL,
		"websiteKey": t.WebsiteKey,
	}
	if t.Proxy != "" {
		body["type"] = "TurnstileTask"
		body["proxy"] = t.Proxy
	}
	if t.Action != "" {
		body["action"] = t.Action
	}
	if t.CData != "" {
		body["cdata"] = t.CData
	}
	// One key per task: a retry after a lost reply returns this task instead of making another.
	current, err := c.request(ctx, http.MethodPost, "/v1/tasks", body, newKey())
	if err != nil {
		return "", err
	}
	interval := c.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for current.Status == "queued" || current.Status == "running" {
		select {
		case <-ctx.Done():
			return "", &Error{Code: "timeout", Message: fmt.Sprintf("task %s was still %s", current.ID, current.Status)}
		case <-time.After(interval):
		}
		if current, err = c.request(ctx, http.MethodGet, "/v1/tasks/"+current.ID, nil, ""); err != nil {
			return "", err
		}
	}
	if current.Status == "succeeded" && current.Solution != nil && current.Solution.Token != "" {
		return current.Solution.Token, nil
	}
	code, message := current.ErrorCode, current.ErrorDescription
	if code == "" {
		code = current.Status
	}
	if message == "" {
		message = "the task " + current.Status + "; nothing was charged"
	}
	return "", &Error{Code: code, Message: message}
}

// request sends one call, trying it up to three times with the same Idempotency-Key.
func (c *Client) request(ctx context.Context, method, path string, body any, idempotencyKey string) (*task, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.API, "/")+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		wait := time.Duration(attempt) * time.Second
		resp, err := httpClient.Do(req)
		if err == nil {
			var text []byte
			text, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if err == nil && resp.StatusCode < 300 {
				var t task
				if err = json.Unmarshal(text, &t); err == nil {
					return &t, nil
				}
			} else if err == nil {
				refusal := problem(resp, text)
				retryable := resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 ||
					resp.StatusCode == 504 || (resp.StatusCode == 409 && refusal.Code == "idempotency_key_in_use")
				if !retryable || attempt == attempts {
					return nil, refusal
				}
				if seconds, parseErr := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); parseErr == nil {
					wait = time.Duration(seconds) * time.Second
				}
				err = nil
			}
		}
		if ctx.Err() != nil {
			return nil, &Error{Code: "timeout", Message: method + " " + path + " ran past the deadline"}
		}
		// No answer, or one cut short: the same Idempotency-Key makes a retry safe.
		if err != nil && attempt == attempts {
			return nil, &Error{Code: "network", Message: err.Error()}
		}
		if deadline, ok := ctx.Deadline(); ok && time.Now().Add(wait).After(deadline) {
			return nil, &Error{Code: "timeout", Message: method + " " + path + " ran past the deadline"}
		}
		select {
		case <-ctx.Done():
			return nil, &Error{Code: "timeout", Message: method + " " + path + " ran past the deadline"}
		case <-time.After(wait):
		}
	}
}

// problem reads the API's problem document (RFC 9457): its code, detail and request ID.
func problem(resp *http.Response, text []byte) *Error {
	var fields struct {
		Code      string `json:"code"`
		Title     string `json:"title"`
		Detail    string `json:"detail"`
		RequestID string `json:"request_id"`
	}
	_ = json.Unmarshal(text, &fields) // a proxy's HTML page has none of them
	refusal := &Error{Code: fields.Code, Message: fields.Detail, RequestID: fields.RequestID}
	if refusal.Code == "" {
		refusal.Code = "http_" + strconv.Itoa(resp.StatusCode)
	}
	if refusal.Message == "" {
		refusal.Message = fields.Title
	}
	if refusal.Message == "" {
		refusal.Message = resp.Status
	}
	if refusal.RequestID == "" {
		refusal.RequestID = resp.Header.Get("X-Request-Id")
	}
	return refusal
}

// newKey makes an Idempotency-Key: a random UUID, version 4.
func newKey() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(errors.New("no randomness: " + err.Error()))
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
