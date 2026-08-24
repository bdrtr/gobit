-- shipping_option_rules sorguları.
--
-- Kural, seçeneğin HANGİ KOŞULDA sunulacağını belirler. Koşulun kendisi
-- burada değerlendirilmez; SQL yalnızca satırları taşır, eşleşme servisteki
-- saf fonksiyonda yapılır (bkz. pricing modülündeki matchRule ile aynı ayrım).

-- name: CreateShippingOptionRule :one
INSERT INTO shipping_option_rules (id, shipping_option_id, attribute, operator, rule_values)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetShippingOptionRule :one
SELECT * FROM shipping_option_rules
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListShippingOptionRules :many
SELECT * FROM shipping_option_rules
WHERE shipping_option_id = $1 AND deleted_at IS NULL
ORDER BY id;

-- ListShippingOptionRulesByOptions kuralları BİRDEN ÇOK seçenek için tek turda
-- döner.
--
-- Toplu olması uygunluk listelemesinin N+1 yapmamasını sağlar: aday seçenek
-- sayısı kadar sorgu atmak, sepet her güncellendiğinde ödenen bir bedel olurdu.
-- name: ListShippingOptionRulesByOptions :many
SELECT * FROM shipping_option_rules
WHERE shipping_option_id = ANY (sqlc.arg('option_ids')::text[]) AND deleted_at IS NULL
ORDER BY shipping_option_id, id;

-- SoftDeleteShippingOptionRule kuralı YUMUŞAK siler (plan Bölüm 8).
-- name: SoftDeleteShippingOptionRule :one
UPDATE shipping_option_rules
SET deleted_at = now(),
    updated_at = now()
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;
