package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

func main() {
	const url = "https://www.tcgplayer.com/product/476452/pokemon-sv01-scarlet-and-violet-base-set-scarlet-and-violet-booster-box?page=1&Language=English"

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(
		context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("disable-infobars", true),
			chromedp.Flag("enable-automation", false),
			chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		)...,
	)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	defer cancelTimeout()

	var body string
	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Sleep(10*time.Second), // let React render
		chromedp.OuterHTML("body", &body, chromedp.ByQuery),
	)
	if err != nil {
		log.Fatalf("chromedp: %v", err)
	}

	fmt.Printf("body length: %d\n", len(body))

	// Extract the full price-points section
	start := strings.Index(body, `class="price-points__lower"`)
	if start == -1 {
		fmt.Println("price-points__lower not found")
		return
	}
	// Print 2000 chars from that point
	end := start + 2000
	if end > len(body) {
		end = len(body)
	}
	fmt.Println(body[start:end])
}
