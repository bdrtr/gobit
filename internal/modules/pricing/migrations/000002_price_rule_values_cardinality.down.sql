-- Kısıtı 000001'deki (boş diziyi geçiren) hâline geri alır.
--
-- Geri alma AYNEN eski tanımı yazar; "daha iyi" bir sürüm bırakmak, down'ın
-- şemayı bir önceki sürüme döndürme sözünü bozardı.
ALTER TABLE price_rule DROP CONSTRAINT IF EXISTS price_rule_values_check;

ALTER TABLE price_rule
    ADD CONSTRAINT price_rule_values_check CHECK (array_length(rule_values, 1) >= 1);
