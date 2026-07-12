// extractbench is the offline experiment harness described in
// docs/plans/00-extraction-experiment-platform.md: it walks a directory of
// saved receipt photos, runs a chosen extraction.Strategy against each one
// directly (bypassing the HTTP server, session model, and rate limiter),
// and prints a comparison table. Run it repeatedly while iterating on a
// strategy — it is not an exposed endpoint.
//
//	go run ./cmd/extractbench -strategy=baseline -dir=testdata/receipts
//
// Needs the same environment as the live server (DATABASE_URL, REDIS_URL,
// LLM_API_KEY, etc. — see internal/config.Load), so it's typically run via
// `docker compose exec backend go run ./cmd/extractbench ...` rather than
// on the host.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"share/backend/internal/config"
	"share/backend/internal/extraction"
	"share/backend/internal/extraction/baseline"
	"share/backend/internal/llm"
	"share/backend/internal/llm/fireworks"
	"share/backend/internal/llm/openai"
)

type expectedEntry struct {
	SubtotalCents int64 `json:"subtotalCents"`
}

func main() {
	strategyFlag := flag.String("strategy", "baseline", "extraction strategy to run (baseline, ...)")
	dirFlag := flag.String("dir", "testdata/receipts", "directory of receipt image files to run against")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	var llmProvider llm.Provider
	switch cfg.LLMProvider {
	case "fireworks":
		llmProvider = fireworks.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	case "openai":
		llmProvider = openai.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
	default:
		log.Fatalf("unknown LLM_PROVIDER %q", cfg.LLMProvider)
	}

	var strategy extraction.Strategy
	switch *strategyFlag {
	case "baseline":
		strategy = baseline.New(llmProvider, cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	default:
		log.Fatalf("unknown -strategy %q", *strategyFlag)
	}

	expected := loadExpected(*dirFlag)

	files, err := receiptFiles(*dirFlag)
	if err != nil {
		log.Fatalf("reading -dir %q: %v", *dirFlag, err)
	}
	if len(files) == 0 {
		log.Fatalf("no .jpg/.jpeg files found in %q", *dirFlag)
	}

	fmt.Printf("%-28s %10s %10s %6s %12s %8s %6s %10s %5s\n",
		"file", "subtotal", "computed", "match", "expected-Δ", "tokens", "cost¢", "latency", "calls")

	var totalCostCents int
	var totalLatency time.Duration
	var matchCount int

	for _, name := range files {
		path := filepath.Join(*dirFlag, name)
		image, err := os.ReadFile(path)
		if err != nil {
			log.Printf("%s: read error: %v", name, err)
			continue
		}
		mimeType := http.DetectContentType(image)

		start := time.Now()
		result, err := strategy.Run(context.Background(), image, mimeType)
		latency := time.Since(start)
		totalLatency += latency

		if err != nil {
			fmt.Printf("%-28s ERROR: %v\n", name, err)
			continue
		}

		computed := computedSubtotalCents(result.Receipt.Items)
		matchMark := "✗"
		if result.SubtotalMatched != nil && *result.SubtotalMatched {
			matchMark = "✓"
			matchCount++
		}

		expectedDiff := "-"
		if e, ok := expected[name]; ok {
			d := result.Receipt.SubtotalCents - e.SubtotalCents
			expectedDiff = fmt.Sprintf("%+d", d)
		}

		var promptTok, completeTok, costCents int
		for _, a := range result.Attempts {
			promptTok += a.PromptTok
			completeTok += a.CompleteTok
			if a.CostCents != nil {
				costCents += *a.CostCents
			}
		}
		totalCostCents += costCents

		fmt.Printf("%-28s %10d %10d %6s %12s %8d %6d %10s %5d\n",
			name, result.Receipt.SubtotalCents, computed, matchMark, expectedDiff,
			promptTok+completeTok, costCents, latency.Round(time.Millisecond), len(result.Attempts))
	}

	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("match rate: %d/%d   total cost: %d¢   total time: %s\n",
		matchCount, len(files), totalCostCents, totalLatency.Round(time.Millisecond))
}

func computedSubtotalCents(items []llm.ExtractedItem) int64 {
	var sum int64
	for _, it := range items {
		qty := it.Quantity
		if qty <= 0 {
			qty = 1
		}
		sum += int64(float64(it.PriceCents)*qty + 0.5)
	}
	return sum
}

func receiptFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".jpg" || ext == ".jpeg" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

func loadExpected(dir string) map[string]expectedEntry {
	b, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		return nil
	}
	var m map[string]expectedEntry
	if err := json.Unmarshal(b, &m); err != nil {
		log.Printf("expected.json: %v (ignoring ground truth)", err)
		return nil
	}
	return m
}
