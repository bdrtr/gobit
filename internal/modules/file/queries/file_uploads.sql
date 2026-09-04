-- file_uploads queries — THE UPLOAD LEDGER.
--
-- The ledger is written AFTER the file itself: first it is written to the
-- provider, then the record is opened. The reverse order — the record first,
-- the write afterwards — would leave a window in which the file the record
-- points at does not exist at all, and every address served in that window
-- would return 404. The only inconsistency this order can produce is an object
-- that HAS ITS FILE but no record: unreachable, but it breaks nothing.

-- name: CreateFileUpload :one
INSERT INTO file_uploads (
    id, storage_key, provider_id, content_type, size, checksum,
    original_name, url, uploaded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetFileUpload :one
SELECT * FROM file_uploads
WHERE id = $1;

-- GetFileUploadByKey is the SERVING path's only query.
--
-- The key coming from the address bar is asked HERE first; if there is no row
-- the file system is not touched at all. That way the only key that can reach
-- the disk is the one this module produced itself and wrote into the ledger —
-- that is what guarantees that the only things served are uploaded files.
-- name: GetFileUploadByKey :one
SELECT * FROM file_uploads
WHERE storage_key = $1;

-- name: ListFileUploads :many
SELECT * FROM file_uploads
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::bigint OFFSET sqlc.arg('row_offset')::bigint;

-- CountFileUploads gives the total count of the pagination envelope.
--
-- The total cannot be read from a window function returned together with the
-- rows: on an out-of-range page no row comes back at all, the window is not
-- evaluated and the total would look like 0.
-- name: CountFileUploads :one
SELECT COUNT(*) FROM file_uploads;

-- DeleteFileUpload deletes the record PERMANENTLY and reports how many rows it
-- deleted.
--
-- Zero rows is NOT an error, and the caller does not treat it as one either:
-- deleting is a claim about an END STATE ("this upload no longer exists") and
-- on the second round of a retried cleanup flow the row is already gone.
-- name: DeleteFileUpload :execrows
DELETE FROM file_uploads
WHERE id = $1;
