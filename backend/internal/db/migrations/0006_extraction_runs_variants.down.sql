ALTER TABLE extraction_runs
  DROP COLUMN strategy,
  DROP COLUMN attempt,
  DROP COLUMN prompt_tokens,
  DROP COLUMN completion_tokens,
  DROP COLUMN cost_cents,
  DROP COLUMN subtotal_matched,
  DROP COLUMN subtotal_diff_cents;
