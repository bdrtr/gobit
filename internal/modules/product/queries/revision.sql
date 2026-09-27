-- name: SetProductVersion :execrows
-- SetProductVersion stamps the version of the revision just appended; the
-- caller holds the product's row lock (ADR 0221).
UPDATE product SET version = sqlc.arg('version')
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: InsertProductRevision :exec
INSERT INTO product_revision (id, product_id, version, recorded_at, changed, request_id, snapshot)
VALUES (
    sqlc.arg('id'), sqlc.arg('product_id'), sqlc.arg('version'), sqlc.arg('recorded_at'),
    sqlc.arg('changed')::text[], sqlc.narg('request_id'), sqlc.arg('snapshot')
);

-- name: GetLatestProductRevision :one
SELECT * FROM product_revision
WHERE product_id = $1
ORDER BY version DESC
LIMIT 1;

-- name: GetProductRevision :one
SELECT * FROM product_revision
WHERE product_id = sqlc.arg('product_id') AND version = sqlc.arg('version');

-- name: ListProductRevisions :many
-- A product's revisions, newest first, without their snapshots.
SELECT id, product_id, version, recorded_at, changed, request_id FROM product_revision
WHERE product_id = sqlc.arg('product_id')
ORDER BY version DESC
LIMIT sqlc.arg('row_limit') OFFSET sqlc.arg('row_offset');

-- name: CountProductRevisions :one
SELECT count(*) FROM product_revision
WHERE product_id = $1;

-- name: RestoreProductContent :execrows
-- RestoreProductContent writes back a revision's descriptive fields exactly,
-- NULLs included, which the PATCH statement's COALESCE cannot (ADR 0221). The
-- status, the schedule and the gift card flag are not a revision's to restore.
UPDATE product SET
    handle         = sqlc.arg('handle'),
    title          = sqlc.arg('title'),
    subtitle       = sqlc.narg('subtitle'),
    description    = sqlc.narg('description'),
    thumbnail      = sqlc.narg('thumbnail'),
    discountable   = sqlc.arg('discountable'),
    weight         = sqlc.narg('weight'),
    length         = sqlc.narg('length'),
    height         = sqlc.narg('height'),
    width          = sqlc.narg('width'),
    material       = sqlc.narg('material'),
    origin_country = sqlc.narg('origin_country'),
    collection_id  = sqlc.narg('collection_id'),
    type_id        = sqlc.narg('type_id'),
    metadata       = sqlc.narg('metadata'),
    updated_at     = now()
WHERE id = sqlc.arg('id') AND deleted_at IS NULL;

-- name: ListLiveTagIDs :many
-- The given tags that are not removed, for a restore to leave out the rest.
SELECT id FROM product_tag
WHERE id = ANY(sqlc.arg('ids')::text[]) AND deleted_at IS NULL;

-- name: ListLiveCategoryIDs :many
-- The given categories that are not removed, for a restore to leave out the rest.
SELECT id FROM product_category
WHERE id = ANY(sqlc.arg('ids')::text[]) AND deleted_at IS NULL;
