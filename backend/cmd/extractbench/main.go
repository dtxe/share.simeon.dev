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
	"share/backend/internal/extraction/cropdetect"
	"share/backend/internal/extraction/deterministic"
	"share/backend/internal/extraction/feedback"
	"share/backend/internal/extraction/imagecroppreprocess"
	"share/backend/internal/extraction/ocrfirst"
	"share/backend/internal/extraction/preprocess"
	"share/backend/internal/imageprep"
	"share/backend/internal/llm"
	"share/backend/internal/llm/fireworks"
	"share/backend/internal/llm/openai"
	"share/backend/internal/llm/openaicompat"
	"share/backend/internal/ocr"
)

type expectedEntry struct {
	SubtotalCents int64 `json:"subtotalCents"`
}

func main() {
	strategyFlag := flag.String("strategy", "baseline", "extraction strategy to run (baseline, deterministic_check, feedback_retry, ocr_first, image_preprocess, image_crop_preprocess)")
	dirFlag := flag.String("dir", "testdata/receipts", "directory of receipt image files to run against")
	dumpOCRFlag := flag.Bool("dump-ocr", false, "print raw OCR text per file before structuring (ocr_first only)")
	dumpCropsFlag := flag.String("dump-crops", "", "write cropped receipt artifacts to this directory (image_crop_preprocess only)")
	flag.Parse()

	if *dumpOCRFlag && *strategyFlag != "ocr_first" {
		log.Fatalf("-dump-ocr is only valid with -strategy=ocr_first (got -strategy=%q)", *strategyFlag)
	}
	if *dumpCropsFlag != "" && *strategyFlag != "image_crop_preprocess" {
		log.Fatalf("-dump-crops is only valid with -strategy=image_crop_preprocess (got -strategy=%q)", *strategyFlag)
	}

	if *dumpCropsFlag != "" {
		if err := os.MkdirAll(*dumpCropsFlag, 0755); err != nil {
			log.Fatalf("creating -dump-crops directory %q: %v", *dumpCropsFlag, err)
		}
	}

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

	ocrEngine := ocr.New()

	var strategy extraction.Strategy
	switch *strategyFlag {
	case "baseline":
		strategy = baseline.New(llmProvider, cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "deterministic_check":
		llmClient := openaicompat.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		strategy = deterministic.New(llmClient, llmProvider.Name(), cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "feedback_retry":
		llmClient := openaicompat.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		strategy = feedback.New(llmClient, llmProvider.Name(), cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "ocr_first":
		llmClient := openaicompat.New(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		strategy = ocrfirst.New(ocrEngine, llmClient, llmProvider.Name(), cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "image_preprocess":
		strategy = preprocess.New(llmProvider, cfg.LLMModel, cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents)
	case "image_crop_preprocess":
		if cfg.CropLLMBaseURL == "" || cfg.CropLLMModel == "" || cfg.CropLLMAPIKey == "" {
			log.Fatalf("image_crop_preprocess requires all of CROP_LLM_BASE_URL, CROP_LLM_MODEL, CROP_LLM_API_KEY to be set explicitly")
		}
		if cfg.CropLLMInputCostPer1KTokensCents <= 0 || cfg.CropLLMOutputCostPer1KTokensCents <= 0 {
			log.Fatalf("image_crop_preprocess requires both CROP_LLM_INPUT_COST_PER_1K_TOKENS_CENTS and CROP_LLM_OUTPUT_COST_PER_1K_TOKENS_CENTS to be set to positive values")
		}
		cropClient := openaicompat.New(cfg.CropLLMBaseURL, cfg.CropLLMAPIKey, cfg.CropLLMModel)
		thinkingBudget := 0
		if openaicompat.SupportsThinking(cfg.CropLLMModel) {
			thinkingBudget = openaicompat.MinThinkingBudgetTokens
		}
		detector := &imagecroppreprocess.RealDetector{
			Client:         cropClient,
			Model:          cfg.CropLLMModel,
			ThinkingBudget: thinkingBudget,
		}
		strategy = imagecroppreprocess.New(
			detector,
			llmProvider.Name()+"_crop", cfg.CropLLMModel,
			cfg.CropLLMInputCostPer1KTokensCents, cfg.CropLLMOutputCostPer1KTokensCents,
			llmProvider, cfg.LLMModel,
			cfg.LLMInputCostPer1KTokensCents, cfg.LLMOutputCostPer1KTokensCents,
		)
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

		if *dumpOCRFlag {
			text, err := ocrEngine.Extract(context.Background(), image)
			if err != nil {
				fmt.Printf("--- %s: ocr error: %v ---\n", name, err)
			} else {
				fmt.Printf("--- %s: raw OCR text ---\n%s\n--- end %s ---\n", name, text, name)
			}
		}

		start := time.Now()
		result, err := strategy.Run(context.Background(), image, mimeType)
		latency := time.Since(start)
		totalLatency += latency

		if err != nil {
			fmt.Printf("%-28s ERROR: %v\n", name, err)
			continue
		}

		computed := extraction.SumItemsCents(result.Receipt.Items)
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

		// Write cropped JPEG + detection metadata when -dump-crops is set.
		if *dumpCropsFlag != "" && *strategyFlag == "image_crop_preprocess" {
			dumpBase := filepath.Join(*dumpCropsFlag, strings.TrimSuffix(name, filepath.Ext(name)))
			if len(result.Attempts) > 0 && len(result.Attempts[0].RawJSON) > 0 {
				detJSON := result.Attempts[0].RawJSON
				// Write detection JSON.
				jsonPath := dumpBase + ".crop.json"
				if err := os.WriteFile(jsonPath, detJSON, 0644); err != nil {
					log.Printf("%s: writing crop JSON: %v", name, err)
				}
				// Unmarshal bounds and write cropped JPEG.
				var detResult cropdetect.DetectionResult
				if err := json.Unmarshal(detJSON, &detResult); err == nil {
					cropped, err := imageprep.Crop(image, imageprep.CropBounds{
						MinX: detResult.Bounds.MinX,
						MinY: detResult.Bounds.MinY,
						MaxX: detResult.Bounds.MaxX,
						MaxY: detResult.Bounds.MaxY,
					})
					if err != nil {
						log.Printf("%s: cropping for dump: %v", name, err)
					} else {
						jpgPath := dumpBase + ".crop.jpg"
						if err := os.WriteFile(jpgPath, cropped, 0644); err != nil {
							log.Printf("%s: writing cropped JPEG: %v", name, err)
						}
					}
				}
			}
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
