-- go-webauthn needs the full Credential struct back (attestation flags,
-- clone-detection data, etc.) to validate future logins — storing only a
-- few extracted fields (public key, sign count) isn't enough to round-trip
-- it faithfully. Store the library's own JSON encoding as the source of
-- truth; credential_id stays a separate indexed column purely for the
-- uniqueness constraint and fast lookups.
ALTER TABLE webauthn_credentials
  DROP COLUMN public_key,
  DROP COLUMN sign_count,
  DROP COLUMN transports,
  ADD COLUMN credential_json JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE webauthn_credentials ALTER COLUMN credential_json DROP DEFAULT;
