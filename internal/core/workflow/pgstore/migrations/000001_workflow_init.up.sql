-- Workflow motorunun yürütme durumu (plan Bölüm 5.5).
--
-- Sahip: "workflow" çekirdek bileşeni. Buradaki tek yabancı anahtar AYNI
-- sahibin iki tablosu arasındadır; modüller arası FK yasağı (Prensip 2.2)
-- ihlal edilmez.

CREATE TABLE workflow_executions (
    -- id "wfx_" önekli, zaman sıralı kimliktir; uygulama üretir.
    id              TEXT        PRIMARY KEY,
    workflow        TEXT        NOT NULL,
    -- idempotency_key isteğe bağlıdır: anahtarsız yürütmeler NULL taşır.
    -- Boş dize saklanmaz; "anahtar yok" durumunun TEK gösterimi NULL'dır.
    idempotency_key TEXT,
    status          TEXT        NOT NULL,
    -- input/output iş verisidir; JSONB olarak saklanır. NULL "değer yok"
    -- demektir ve JSON'un kendi null değerinden ('null') farklıdır.
    input           JSONB,
    output          JSONB,
    failure         TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workflow_executions_workflow_not_blank
        CHECK (workflow <> ''),
    CONSTRAINT workflow_executions_status_not_blank
        CHECK (status <> ''),
    -- CHECK NULL'da doğrulanmaz; bu kısıt yalnızca boş dizeyi eler.
    CONSTRAINT workflow_executions_idempotency_key_not_blank
        CHECK (idempotency_key <> '')
);

-- Idempotency yalnızca DOLU anahtarlar için benzersizdir. Kısmi indeks
-- (WHERE ... IS NOT NULL) bunu açıkça söyler; ayrıca NULL'lar birbiriyle
-- çakışmadığı için anahtarsız yürütmeler serbestçe açılabilir.
--
-- Yarış koşuluna dayanıklılık bu indekse dayanır: iki süreç aynı anda
-- ekleme yaparsa biri 23505 alır ve uygulama bunu Conflict'e çevirir.
-- "Önce SELECT sonra INSERT" bu güvenceyi veremezdi.
CREATE UNIQUE INDEX workflow_executions_idempotency_key_uniq
    ON workflow_executions (workflow, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Bir workflow'un son yürütmelerini listelemek yaygın erişim biçimidir.
CREATE INDEX workflow_executions_workflow_created_at_idx
    ON workflow_executions (workflow, created_at DESC);

CREATE TABLE workflow_execution_steps (
    execution_id TEXT        NOT NULL
                 REFERENCES workflow_executions (id) ON DELETE CASCADE,
    -- Sütun adı step_index'tir: "index" PostgreSQL'de anahtar sözcüktür ve
    -- tırnaklanmadan kullanılamaz; tırnaklı ad ise her sorguda tuzaktır.
    step_index   INTEGER     NOT NULL,
    name         TEXT        NOT NULL,
    status       TEXT        NOT NULL,
    output       JSONB,
    failure      TEXT        NOT NULL DEFAULT '',
    -- attempts retry sayacıdır; aynı adım yeniden denendiğinde artar.
    attempts     INTEGER     NOT NULL DEFAULT 0,
    -- Zamanlar ölçülmemişse NULL'dır (sıfır time.Time'ın karşılığı).
    started_at   TIMESTAMPTZ,
    ended_at     TIMESTAMPTZ,
    -- (execution_id, step_index) birincil anahtardır: aynı adıma ikinci kez
    -- yazmak yeni satır AÇMAZ, var olanı günceller (ON CONFLICT ile).
    PRIMARY KEY (execution_id, step_index),
    CONSTRAINT workflow_execution_steps_step_index_non_negative
        CHECK (step_index >= 0),
    CONSTRAINT workflow_execution_steps_attempts_non_negative
        CHECK (attempts >= 0),
    CONSTRAINT workflow_execution_steps_name_not_blank
        CHECK (name <> ''),
    CONSTRAINT workflow_execution_steps_status_not_blank
        CHECK (status <> '')
);
