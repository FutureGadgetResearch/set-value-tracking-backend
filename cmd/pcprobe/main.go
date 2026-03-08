// pcprobe probes PriceCharting URLs for Pokemon Hidden Fates cards whose
// official set numbers differ from PriceCharting's internal numbering.
//
// Usage:  go run ./cmd/pcprobe
package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const baseURL = "https://www.pricecharting.com/game/pokemon-hidden-fates/"

// candidate describes one card we need to locate.
type candidate struct {
	officialNum string
	name        string   // URL slug prefix, e.g. "raichu-gx"
	testNums    []string // PC SV numbers (or plain card numbers) to try
}

func buildSVRange(lo, hi int) []string {
	var out []string
	for i := lo; i <= hi; i++ {
		out = append(out, fmt.Sprintf("sv%d", i))
	}
	return out
}

func main() {
	// The 10 shiny GX cards (official SV71-SV80) plus 4 secret rares (SV91-SV94)
	// and the Moltres/Zapdos/Articuno GX at #66.
	//
	// Known offset info from PC console:
	//   PC sv71 = Guzzlord GX   (extra card inserted)
	//   PC sv72 = Scizor GX     (official SV75 = Scizor GX)
	//   PC sv75 = Gardevoir GX  (official SV77 = Gardevoir GX)
	//   PC sv76 = Sylveon GX    (extra card)
	//
	// So there appear to be extra cards interspersed. We test a wide range
	// (sv71 – sv110) for each name to be safe.
	svRange := buildSVRange(71, 110)

	cards := []candidate{
		// Official SV71-SV80 shiny GX
		{officialNum: "SV71", name: "raichu-gx", testNums: svRange},
		{officialNum: "SV72", name: "gengar-gx", testNums: svRange},
		{officialNum: "SV73", name: "gyarados-gx", testNums: svRange},
		{officialNum: "SV74", name: "tapu-lele-gx", testNums: svRange},
		{officialNum: "SV75", name: "scizor-gx", testNums: svRange},
		{officialNum: "SV76", name: "noivern-gx", testNums: svRange},
		{officialNum: "SV77", name: "gardevoir-gx", testNums: svRange},
		{officialNum: "SV78", name: "marshadow-gx", testNums: svRange},
		{officialNum: "SV79", name: "incineroar-gx", testNums: svRange},
		{officialNum: "SV80", name: "magcargo-gx", testNums: svRange},
		// Official SV91-SV94 secret rares
		{officialNum: "SV91", name: "pikachu", testNums: buildSVRange(88, 110)},
		{officialNum: "SV92", name: "charizard-gx", testNums: buildSVRange(88, 110)},
		{officialNum: "SV93", name: "mewtwo-gx", testNums: buildSVRange(88, 110)},
		{officialNum: "SV94", name: "rayquaza-gx", testNums: buildSVRange(88, 110)},
	}

	// Also test the Moltres & Zapdos & Articuno GX with ampersand encoding.
	specialURLs := []string{
		baseURL + "moltres-&-zapdos-&-articuno-gx-66",
		baseURL + "moltres-%26-zapdos-%26-articuno-gx-66",
		baseURL + "moltres-zapdos-articuno-gx-66",
	}

	fmt.Println("=== Moltres & Zapdos & Articuno GX (#66) ===")
	for _, u := range specialURLs {
		status, finalURL := probe(u)
		fmt.Printf("  %-75s  ->  %s  [%s]\n", u, status, finalURL)
	}
	fmt.Println()

	results := make(map[string]string) // officialNum -> found URL

	for _, card := range cards {
		fmt.Printf("=== %s  %s ===\n", card.officialNum, card.name)
		found := false
		for _, num := range card.testNums {
			u := baseURL + card.name + "-" + num
			status, finalURL := probe(u)
			marker := ""
			if status == "200 OK" {
				marker = "  *** FOUND ***"
				results[card.officialNum] = u
				found = true
			}
			fmt.Printf("  %-70s  ->  %-12s  %s  [%s]\n", u, status, marker, finalURL)
			if found {
				break
			}
		}
		if !found {
			fmt.Printf("  ** NOT FOUND in tested range **\n")
		}
		fmt.Println()
	}

	fmt.Println("=== SUMMARY (official number -> confirmed PC URL) ===")
	if len(results) == 0 {
		fmt.Println("  No confirmed matches found.")
	}
	for _, card := range cards {
		if u, ok := results[card.officialNum]; ok {
			fmt.Printf("  %s  %-20s  %s\n", card.officialNum, card.name, u)
		} else {
			fmt.Printf("  %s  %-20s  NOT FOUND\n", card.officialNum, card.name)
		}
	}
}

// probe issues a GET to url (with a browser User-Agent) and returns the HTTP
// status string plus the final URL after any redirect. It retries on 429.
func probe(url string) (status string, finalURL string) {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// allow redirects but record them
			return nil
		},
	}

	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return "BUILD_ERR", url
		}
		req.Header.Set("User-Agent",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
				"AppleWebKit/537.36 (KHTML, like Gecko) "+
				"Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")

		resp, err := client.Do(req)
		if err != nil {
			return "NET_ERR:" + err.Error(), url
		}
		finalURL = resp.Request.URL.String()
		resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			wait := 60 * time.Second
			ra := resp.Header.Get("Retry-After")
			if ra != "" {
				var secs int
				if n, _ := fmt.Sscanf(ra, "%d", &secs); n == 1 && secs > 0 {
					wait = time.Duration(secs) * time.Second
				}
			}
			fmt.Printf("    [429 rate-limited – waiting %s, attempt %d/%d]\n",
				wait.Round(time.Second), attempt, maxRetries)
			time.Sleep(wait)
			continue
		}

		// Classify.
		switch resp.StatusCode {
		case 200:
			// Check if the final URL differs significantly (i.e. PC redirected
			// us to a different card page, meaning this slug doesn't exist).
			if !strings.Contains(finalURL, strings.TrimSuffix(url, "/")) &&
				finalURL != url {
				return fmt.Sprintf("REDIRECT->%d", resp.StatusCode), finalURL
			}
			return "200 OK", finalURL
		case 301, 302, 303, 307, 308:
			return fmt.Sprintf("REDIRECT %d", resp.StatusCode), finalURL
		case 404:
			return "404 NOT_FOUND", finalURL
		default:
			return fmt.Sprintf("HTTP_%d", resp.StatusCode), finalURL
		}
	}
	return "429_EXHAUSTED", url
}
