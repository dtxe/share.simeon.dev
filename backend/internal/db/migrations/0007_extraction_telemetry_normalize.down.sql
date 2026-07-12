-- 0007 DOWN: revert extraction telemetry normalization.
--
-- Denormalizes parent values back into extraction_attempts, removes the run
-- relation, drops the parent table, and renames extraction_attempts back to
-- extraction_runs.

-- (Step 1) re-add columns that were normalized upward
ALTER TABLE extraction_attempts ADD COLUMN session_id UUID REFERENCES bill_sessions(id);
ALTER TABLE extraction_attempts ADD COLUMN strategy TEXT;

-- (Step 2) denormalize parent values back into attempts
UPDATE extraction_attempts ea
SET
    session_id = er.session_id,
    strategy   = er.strategy
FROM extraction_runs er
WHERE ea.run_id = er.id;

-- (Step 3) set NOT NULL and defaults
ALTER TABLE extraction_attempts ALTER COLUMN session_id SET NOT NULL;
ALTER TABLE extraction_attempts ALTER COLUMN strategy SET NOT NULL;
ALTER TABLE extraction_attempts ALTER COLUMN strategy SET DEFAULT 'baseline';

-- (Step 4) remove run relation
DROP INDEX IF EXISTS idx_extraction_attempts_run;
ALTER TABLE extraction_attempts DROP CONSTRAINT IF EXISTS extraction_attempts_run_id_attempt_key;
ALTER TABLE extraction_attempts DROP CONSTRAINT IF EXISTS extraction_attempts_cost_cents_nonnegative;
ALTER TABLE extraction_attempts DROP COLUMN run_id;
ALTER TABLE extraction_attempts ALTER COLUMN subtotal_diff_cents TYPE INT;

-- (Step 5) drop parent table
DROP TABLE IF EXISTS extraction_runs;

-- (Step 6) rename attempts back to extraction_runs
ALTER TABLE extraction_attempts RENAME TO extraction_runs;
ALTER INDEX extraction_attempts_pkey RENAME TO extraction_runs_pkey;
CREATE INDEX idx_extraction_runs_session ON extraction_runs(session_id);
