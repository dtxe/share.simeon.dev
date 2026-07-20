ALTER TABLE extraction_attempts
    ALTER COLUMN raw_response TYPE JSONB USING
        CASE WHEN raw_response IS NULL THEN NULL
             ELSE to_jsonb(encode(raw_response, 'base64'))
        END;
