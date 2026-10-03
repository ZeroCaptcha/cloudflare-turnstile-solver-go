<!-- zc:header (generated from the registry; edit repos/registry.json) -->
# Cloudflare Turnstile solver in Go

[![CI](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-go/actions/workflows/ci.yml/badge.svg)](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-go/actions/workflows/ci.yml)

Solve Cloudflare Turnstile in Go: a tested example that gets a Cloudflare Turnstile token from the ZeroCaptcha API with net/http, context deadlines, retries and idempotency keys. Standard library only, Go 1.22+.

[Website](https://zerocaptcha.io/cloudflare-turnstile-solver/go) · [Docs](https://zerocaptcha.io/docs) · [Quickstart](https://zerocaptcha.io/docs/quickstart) · [API reference](https://zerocaptcha.io/docs/reference/api) · [Pricing](https://zerocaptcha.io/pricing)
<!-- /zc:header -->

## What it does

Package `turnstile` gets a valid Cloudflare Turnstile token for a page you are allowed to automate, and `cmd/solve-turnstile` prints one from the command line. You give it the page's URL and the widget's sitekey; it creates a task on the ZeroCaptcha API, waits for it, and returns the token to submit as the browser would.

- **Standard library only:** `net/http`, `encoding/json` and `crypto/rand`. Go 1.22 or later.
- **Context-aware:** the wait ends when your context does; give it a deadline, such as three minutes.
- **Safe to retry:** every task is created with its own `Idempotency-Key`, so a retry after a lost reply returns the same task instead of paying for a second one. 429, 502, 503 and 504 are retried after the wait the API asks for.
- **Typed failures:** a refusal or a failed task is a `*turnstile.Error` with the API's `Code`, such as `insufficient_funds` or `ERROR_CAPTCHA_UNSOLVABLE`, and the `RequestID` to quote to support.

## Quickstart

1. Create an account on the ZeroCaptcha website, create an API key on the dashboard and add funds (crypto, from $10). A task is charged only when it succeeds.
2. Put the API's address and your key in your environment, never in your code:

   ```sh
   export ZEROCAPTCHA_API=https://api.zerocaptcha.io
   export ZEROCAPTCHA_KEY=zc_live_...
   ```

3. Read the widget's `data-sitekey`, and its `data-action` and `data-cdata` if it sets them (the [sitekey guide](https://zerocaptcha.io/guides/find-cloudflare-turnstile-sitekey) shows where else they hide, such as the options of `turnstile.render()`), then run:

   ```sh
   go run ./cmd/solve-turnstile https://shop.example.com/login 0x4AAAAAAAB1cD2eF3gH4iJ5 login session-7f3a9c2e
   ```

   Many sites check the action and cData when they verify the token, so pass both whenever the widget sets them.

   It prints the token. Set `PROXY_URL=http://user:pass@proxy.example.net:8080` to solve through your own proxy.

## Use it in your code

```sh
go get github.com/zerocaptcha/cloudflare-turnstile-solver-go
```

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
defer cancel()

client := &turnstile.Client{API: os.Getenv("ZEROCAPTCHA_API"), Key: os.Getenv("ZEROCAPTCHA_KEY")}
token, err := client.Solve(ctx, turnstile.Task{
	WebsiteURL: "https://shop.example.com/login",
	WebsiteKey: "0x4AAAAAAAB1cD2eF3gH4iJ5",
	// The widget's data-action and data-cdata, or turnstile.render()'s action and cData options;
	// leave out any the widget does not set.
	Action: "login",
	CData:  "session-7f3a9c2e",
	// Proxy: "http://user:pass@proxy.example.net:8080", // to solve through your own proxy
})
var failed *turnstile.Error
if errors.As(err, &failed) {
	log.Fatalf("%s (request %s)", failed.Code, failed.RequestID)
}
```

## Submit the token

The widget sends its token in the `cf-turnstile-response` form field, and the site checks it with Cloudflare's siteverify when the form arrives. Send yours the same way, straight after you get it:

```go
form := url.Values{"email": {"me@example.com"}, "cf-turnstile-response": {token}}
resp, err := http.PostForm("https://shop.example.com/login", form)
```

With colly it is the same field: see the [Go colly tutorial](https://zerocaptcha.io/blog/go-colly-cloudflare-turnstile).

## How it works

1. `POST /v1/tasks` with the page, the sitekey, and the action and cData if the widget sets them. The task's price is held on your balance.
2. `GET /v1/tasks/{id}` every 2 seconds while the task is `queued` or `running`.
3. `succeeded` carries `solution.token`, and the held price is charged. `failed` or `expired` carries an `errorCode`, and the hold is released: nothing is charged.

The [task lifecycle](https://zerocaptcha.io/docs/how-tasks-work) and the [errors and retries guide](https://zerocaptcha.io/docs/errors-and-retries) have every detail.

## Honest limits

- **A token works once, for 300 seconds.** Get it just before you submit, and a new one for the next submission.
- **The action and cData must match the widget's.** A token made without them can be refused by the site's siteverify check.
- **Proxies are `http` or `https`,** with the port in the URL. SOCKS is not supported.
- **Only for sites you own or are allowed to automate.** The [Acceptable Use Policy](https://zerocaptcha.io/legal/acceptable-use) applies to every task.
- **This is an example, not a library.** For a maintained client with callbacks, challenge pages and signature checks, use the official [Go SDK](https://github.com/ZeroCaptcha/zerocaptcha-go).

## FAQ

**What does a solve cost?**
The [pricing page](https://zerocaptcha.io/pricing) lists the price per 1,000 solved tasks. Only a task that succeeds is charged.

**How long does a solve take?**
It varies with the site and the load. The [status page](https://zerocaptcha.io/status) shows the live figures for the last 24 hours.

**Why is my token refused by the site?**
Most often it was used twice, used after 300 seconds, or made without the widget's action or cData. The [siteverify errors article](https://zerocaptcha.io/blog/cloudflare-turnstile-siteverify-errors) explains each code.

**Can I run many solves at once?**
Yes: `Client` is safe to share between goroutines, and paid tasks have no rate limit. Reads have a budget, so keep the 2-second interval; see [rate limits](https://zerocaptcha.io/docs/rate-limits).

## Run the tests

```sh
go vet ./...
go test ./...
```

The tests run the solver against a stand-in API (`net/http/httptest`) on your machine: no key, no real task, nothing spent.

<!-- zc:footer (generated from the registry) -->
## More from ZeroCaptcha

- The website: [ZeroCaptcha](https://zerocaptcha.io), the [docs](https://zerocaptcha.io/docs), the [guides](https://zerocaptcha.io/guides), the [blog](https://zerocaptcha.io/blog) and the [status page](https://zerocaptcha.io/status)
- Start here: [zerocaptcha](https://github.com/ZeroCaptcha/zerocaptcha), [cloudflare-turnstile-solver](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver), [cloudflare-challenge-solver](https://github.com/ZeroCaptcha/cloudflare-challenge-solver)
- Examples by language: [cloudflare-turnstile-solver-python](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-python), [cloudflare-turnstile-solver-nodejs](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-nodejs), **cloudflare-turnstile-solver-go**, [cloudflare-turnstile-solver-php](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-php), [cloudflare-turnstile-solver-java](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-java), [cloudflare-turnstile-solver-csharp](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-csharp), [cloudflare-turnstile-solver-rust](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-rust)
- Browser automation: [cloudflare-turnstile-solver-playwright](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-playwright), [cloudflare-turnstile-solver-puppeteer](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-puppeteer), [cloudflare-turnstile-solver-selenium](https://github.com/ZeroCaptcha/cloudflare-turnstile-solver-selenium)
- SDKs, MCP server and migration: [zerocaptcha-js](https://github.com/ZeroCaptcha/zerocaptcha-js), [zerocaptcha-python](https://github.com/ZeroCaptcha/zerocaptcha-python), [zerocaptcha-go](https://github.com/ZeroCaptcha/zerocaptcha-go), [zerocaptcha-mcp](https://github.com/ZeroCaptcha/zerocaptcha-mcp), [createtask-api-migration](https://github.com/ZeroCaptcha/createtask-api-migration)
- Lists: [awesome-cloudflare-turnstile](https://github.com/ZeroCaptcha/awesome-cloudflare-turnstile)

## Licence

MIT: see [LICENSE](LICENSE).

## Disclaimer

ZeroCaptcha is an independent service, not affiliated with or endorsed by Cloudflare. Cloudflare and Turnstile are trademarks of Cloudflare, Inc. Use ZeroCaptcha only on sites you own or are allowed to automate, as the [Acceptable Use Policy](https://zerocaptcha.io/legal/acceptable-use) says; any site owner can [opt out](https://zerocaptcha.io/opt-out).
<!-- /zc:footer -->
