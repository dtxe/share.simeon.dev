// Package extraction sits above internal/llm.Provider (which stays the
// low-level "one chat-completions call" abstraction) and defines
// swappable receipt-extraction strategies. Selected via EXTRACTION_STRATEGY
// (internal/config), mirroring the existing LLM_PROVIDER pattern.
package extraction

import (
	"context"

	"share/backend/internal/llm"
)

// Attempt records one LLM call made during a Run. A strategy that retries
// or takes a multi-call path reports one Attempt per call, so the wrong
// first guess and the corrected second one are both visible.
type Attempt struct {
	Provider    string
	Model       string
	PromptTok   int
	CompleteTok int
	CostCents   int
	RawJSON     []byte // the parsed ExtractedReceipt, marshaled back
	Err         error  // non-nil if this attempt failed
}

type RunResult struct {
	Receipt           llm.ExtractedReceipt
	Attempts          []Attempt
	SubtotalMatched   bool
	SubtotalDiffCents int64
}

type Strategy interface {
	Name() string
	// MaxCalls is the worst-case number of LLM calls this strategy can make
	// for one Run — used to size the upfront spend-cap reservation and the
	// handler's per-call timeout budget.
	MaxCalls() int
	Run(ctx context.Context, image []byte, mimeType string) (*RunResult, error)
}
