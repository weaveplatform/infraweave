-- infraweave:compatible-from 00008
-- Decision 0013 seals a stored response's body, which can hold whatever
-- the request created. A binary older than this file writes the plaintext
-- column, so it refuses to start on this schema. The table is a 24-hour
-- replay cache, so it is emptied rather than resealed: a request retried
-- across the upgrade runs again, as one retried after its key expired does.

DELETE FROM idempotency_keys;

ALTER TABLE idempotency_keys DROP COLUMN body;

-- The response body sealed under the keyring, with the associated data
-- idempotency_keys.body_sealed:<caller>/<key>; NULL while the key is in
-- flight, and for a response with no body.
ALTER TABLE idempotency_keys ADD COLUMN body_sealed bytea;
