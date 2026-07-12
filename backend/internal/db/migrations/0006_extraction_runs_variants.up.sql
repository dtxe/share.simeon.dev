ALTER TABLE extraction_runs
  ADD COLUMN strategy TEXT NOT NULL DEFAULT 'baseline',
  ADD COLUMN attempt INT NOT NULL DEFAULT 1,
  ADD COLUMN prompt_tokens INT,
  ADD COLUMN completion_tokens INT,
  ADD COLUMN cost_cents INT,
  ADD COLUMN subtotal_matched BOOLEAN,
  ADD COLUMN subtotal_diff_cents INT;
