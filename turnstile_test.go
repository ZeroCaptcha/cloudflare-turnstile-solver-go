package turnstile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// The solver against a stand-in API: no key, no real task, nothing spent.

const (
	testKey = "zc_live_test_key"
	taskID  = "0192f3a4-7b1c-7d2e-9f10-3c4d5e6f7a8b"
	token   = "0.stand-in-turnstile-token"
	page    = "https://shop.example.com/login"
	sitekey = "0x4AAAAAAAB1cD2eF3gH4iJ5"
)

type recorded struct {
	method, path, authorization, idempotencyKey string
	body                                        map[string]string
}

// standIn answers POST /v1/tasks and GET /v1/tasks/{id} as the API does, playing one scenario:
// "success", "failed", "rate-limited" (the first create is 429) or "insufficient-funds".
type standIn struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recorded
	creates  int
	polls    int
}

func newStandIn(t *testing.T, scenario string) *standIn {
	s := &standIn{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.requests = append(s.requests, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), body})
		reply := func(status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		problem := func(status int, code string) {
			reply(status, map[string]any{"type": "about:blank", "title": code, "status": status, "detail": "The stand-in answered " + code + ".", "code": code, "request_id": "0192f3a4-0000-7000-8000-000000000000"})
		}
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			problem(401, "unauthorized")
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/tasks":
			s.creates++
			if scenario == "rate-limited" && s.creates == 1 {
				w.Header().Set("Retry-After", "0")
				problem(429, "rate_limited")
				return
			}
			if scenario == "insufficient-funds" {
				problem(402, "insufficient_funds")
				return
			}
			reply(201, map[string]any{"id": taskID, "status": "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/tasks/"+taskID:
			s.polls++
			switch {
			case scenario == "failed":
				reply(200, map[string]any{"id": taskID, "status": "failed", "errorCode": "ERROR_CAPTCHA_UNSOLVABLE", "errorDescription": "Every attempt failed."})
			case s.polls == 1:
				reply(200, map[string]any{"id": taskID, "status": "running"})
			default:
				reply(200, map[string]any{"id": taskID, "status": "succeeded", "solution": map[string]string{"token": token}})
			}
		default:
			problem(404, "not_found")
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *standIn) client() *Client {
	return &Client{API: s.URL, Key: testKey, Interval: 10 * time.Millisecond}
}

func TestSolveReturnsTheToken(t *testing.T) {
	api := newStandIn(t, "success")
	got, err := api.client().Solve(context.Background(), Task{WebsiteURL: page, WebsiteKey: sitekey, Action: "login", CData: "session-7f3a9c2e"})
	if err != nil || got != token {
		t.Fatalf("Solve = %q, %v; want the token", got, err)
	}
	create := api.requests[0]
	if create.method != http.MethodPost || create.path != "/v1/tasks" || create.authorization != "Bearer "+testKey || create.idempotencyKey == "" {
		t.Fatalf("the create was %+v", create)
	}
	// The widget's action and cData reach the API, so a site that checks them accepts the token.
	want := map[string]string{"type": "TurnstileTaskProxyless", "websiteURL": page, "websiteKey": sitekey, "action": "login", "cdata": "session-7f3a9c2e"}
	if len(create.body) != len(want) {
		t.Fatalf("the body was %v, want %v", create.body, want)
	}
	for field, value := range want {
		if create.body[field] != value {
			t.Fatalf("the body's %s was %q, want %q", field, create.body[field], value)
		}
	}
	if len(api.requests) != 3 {
		t.Fatalf("%d requests, want a create and two polls", len(api.requests))
	}
}

func TestAProxyMakesAProxiedTask(t *testing.T) {
	api := newStandIn(t, "success")
	proxy := "http://user:pass@proxy.example.net:8080"
	if _, err := api.client().Solve(context.Background(), Task{WebsiteURL: page, WebsiteKey: sitekey, Proxy: proxy}); err != nil {
		t.Fatal(err)
	}
	if body := api.requests[0].body; body["type"] != "TurnstileTask" || body["proxy"] != proxy {
		t.Fatalf("the body was %v", body)
	}
}

func TestAFailedTaskIsItsCode(t *testing.T) {
	api := newStandIn(t, "failed")
	_, err := api.client().Solve(context.Background(), Task{WebsiteURL: page, WebsiteKey: sitekey})
	var failed *Error
	if !errors.As(err, &failed) || failed.Code != "ERROR_CAPTCHA_UNSOLVABLE" {
		t.Fatalf("err = %v, want ERROR_CAPTCHA_UNSOLVABLE", err)
	}
}

func TestARateLimitedCreateIsRetriedWithTheSameKey(t *testing.T) {
	api := newStandIn(t, "rate-limited")
	if got, err := api.client().Solve(context.Background(), Task{WebsiteURL: page, WebsiteKey: sitekey}); err != nil || got != token {
		t.Fatalf("Solve = %q, %v", got, err)
	}
	first, second := api.requests[0], api.requests[1]
	if first.method != http.MethodPost || second.method != http.MethodPost || first.idempotencyKey != second.idempotencyKey {
		t.Fatalf("the creates were %+v and %+v", first, second)
	}
}

func TestARefusalIsReturnedAtOnce(t *testing.T) {
	api := newStandIn(t, "insufficient-funds")
	_, err := api.client().Solve(context.Background(), Task{WebsiteURL: page, WebsiteKey: sitekey})
	var refused *Error
	if !errors.As(err, &refused) || refused.Code != "insufficient_funds" || refused.RequestID == "" {
		t.Fatalf("err = %#v, want insufficient_funds with a request ID", err)
	}
	if len(api.requests) != 1 {
		t.Fatalf("%d requests, want 1", len(api.requests))
	}
}

func TestTheDeadlineStopsTheWait(t *testing.T) {
	api := newStandIn(t, "success")
	client := api.client()
	client.Interval = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := client.Solve(ctx, Task{WebsiteURL: page, WebsiteKey: sitekey})
	var timeout *Error
	if !errors.As(err, &timeout) || timeout.Code != "timeout" {
		t.Fatalf("err = %v, want timeout", err)
	}
}
