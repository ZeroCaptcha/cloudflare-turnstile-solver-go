// Command solve-turnstile prints a Cloudflare Turnstile token for a page and its sitekey:
//
//	export ZEROCAPTCHA_API=https://api.zerocaptcha.io ZEROCAPTCHA_KEY=zc_live_...
//	go run ./cmd/solve-turnstile https://shop.example.com/login 0x4AAAAAAAB1cD2eF3gH4iJ5 [action] [cdata]
//
// action and cdata are the widget's data-action and data-cdata (or turnstile.render()'s action and
// cData options): pass them whenever the widget sets them, since many sites check both when they
// verify the token. Set PROXY_URL to solve through your own proxy.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	turnstile "github.com/zerocaptcha/cloudflare-turnstile-solver-go"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) < 2 || len(args) > 4 {
		fmt.Fprintln(os.Stderr, "usage: solve-turnstile <page URL> <sitekey> [action] [cdata]")
		return 2
	}
	api, key := os.Getenv("ZEROCAPTCHA_API"), os.Getenv("ZEROCAPTCHA_KEY")
	if api == "" || key == "" {
		fmt.Fprintln(os.Stderr, "Set ZEROCAPTCHA_API and ZEROCAPTCHA_KEY first.")
		return 2
	}
	task := turnstile.Task{WebsiteURL: args[0], WebsiteKey: args[1], Proxy: os.Getenv("PROXY_URL")}
	if len(args) >= 3 {
		task.Action = args[2]
	}
	if len(args) == 4 {
		task.CData = args[3]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := &turnstile.Client{API: api, Key: key}
	token, err := client.Solve(ctx, task)
	var failed *turnstile.Error
	switch {
	case errors.As(err, &failed) && failed.RequestID != "":
		fmt.Fprintf(os.Stderr, "%v (request %s)\n", failed, failed.RequestID)
		return 1
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(token)
	return 0
}
