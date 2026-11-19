-- An unfinished or abandoned server-side upload has no checksum. It may be
-- marked for deletion, but must never become live. The reaper waits for an
-- active writer to finish before removing its row and object.
ALTER TABLE blob_objects DROP CONSTRAINT blob_objects_check;
ALTER TABLE blob_objects ADD CONSTRAINT blob_objects_live_checksum
    CHECK (state <> 'live' OR sha256 IS NOT NULL);
