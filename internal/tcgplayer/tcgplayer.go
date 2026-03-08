package tcgplayer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// CurrentMetrics holds the snapshot of current TCGPlayer listing data.
type CurrentMetrics struct {
	MedianAskPrice float64 // listedMedianPrice for Normal condition (from /pricepoints API)
	ProductCount   int     // total active listings (from price-points__lower__left-padding)
	SellerCount    int     // unique sellers (from price-points__lower__right-padding)
}

// ScrapeCurrentMetrics fetches current listing metrics from TCGPlayer for the
// given product ID. Only the most recent snapshot row should use this.
func ScrapeCurrentMetrics(productID string) (CurrentMetrics, error) {
	productURL := "https://www.tcgplayer.com/product/" + productID

	median, err := fetchMedianAskPrice(productID)
	if err != nil {
		return CurrentMetrics{}, fmt.Errorf("median ask price: %w", err)
	}

	productCount, sellerCount, err := fetchListingCounts(productURL)
	if err != nil {
		return CurrentMetrics{}, fmt.Errorf("listing counts: %w", err)
	}

	return CurrentMetrics{
		MedianAskPrice: median,
		ProductCount:   productCount,
		SellerCount:    sellerCount,
	}, nil
}

type conditionListing struct {
	PrintingType      string  `json:"printingType"`
	ListedMedianPrice float64 `json:"listedMedianPrice"`
}

func fetchMedianAskPrice(productID string) (float64, error) {
	body, err := get("https://mpapi.tcgplayer.com/v2/product/" + productID + "/pricepoints")
	if err != nil {
		return 0, err
	}
	var results []conditionListing
	if err := json.Unmarshal(body, &results); err != nil {
		return 0, fmt.Errorf("parsing pricepoints: %w", err)
	}
	for _, r := range results {
		if r.PrintingType == "Normal" {
			return r.ListedMedianPrice, nil
		}
	}
	return 0, fmt.Errorf("no Normal printing found in pricepoints")
}

// fetchListingCounts uses a headless browser to render the TCGPlayer product
// page and read seller count / listing count from the price-points lower section.
//
// HTML structure (price-points__lower__top-padding row):
//   col 1: "Current Quantity:" label
//   col 2 (price-points__lower__right-padding): quantity value
//   col 3 (price-points__lower__left-padding): "Current Sellers:" label
//   col 4 (no class): seller count value
func fetchListingCounts(productURL string) (productCount, sellerCount int, err error) {
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(
		context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("disable-infobars", true),
			chromedp.Flag("enable-automation", false),
			chromedp.UserAgent(ua),
		)...,
	)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	ctx, cancelTimeout := context.WithTimeout(ctx, 90*time.Second)
	defer cancelTimeout()

	const (
		quantitySel = `tr.price-points__lower__top-padding td.price-points__lower__right-padding span.price-points__lower__price`
		sellersSel  = `tr.price-points__lower__top-padding td:nth-child(4) span.price-points__lower__price`
	)

	var quantityText, sellersText string
	if err = chromedp.Run(ctx,
		chromedp.Navigate(productURL),
		chromedp.WaitVisible(`tr.price-points__lower__top-padding`, chromedp.ByQuery),
		chromedp.Text(quantitySel, &quantityText, chromedp.ByQuery),
		chromedp.Text(sellersSel, &sellersText, chromedp.ByQuery),
	); err != nil {
		return 0, 0, fmt.Errorf("rendering page: %w", err)
	}

	productCount = extractInt(quantityText)
	sellerCount = extractInt(sellersText)
	return productCount, sellerCount, nil
}

// extractInt finds the first integer in a string, stripping commas from numbers.
func extractInt(s string) int {
	s = strings.ReplaceAll(s, ",", "")
	for _, field := range strings.Fields(s) {
		if n, err := strconv.Atoi(field); err == nil {
			return n
		}
	}
	return 0
}

func get(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}
