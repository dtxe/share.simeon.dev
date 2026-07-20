-- Preserve the upstream response bytes verbatim. JSONB normalizes whitespace
-- and cannot represent non-JSON provider error bodies. Existing JSONB values
-- are retained as their textual JSON representation.
ALTER TABLE extraction_attempts
    ALTER COLUMN raw_response TYPE BYTEA USING convert_to(raw_response::text, 'UTF8');
