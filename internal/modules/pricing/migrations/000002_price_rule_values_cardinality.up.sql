-- price_rule.rule_values kısıtını GERÇEKTEN kapatan hâline getirir.
--
-- 000001'deki CHECK (array_length(rule_values, 1) >= 1) boş diziyi geçiriyordu:
-- PostgreSQL'de array_length('{}', 1) NULL döner ve sonucu NULL olan bir CHECK
-- SAĞLANMIŞ sayılır. Kısıt böylece yalnızca NULL sütunu engelliyordu ki onu
-- zaten NOT NULL yapıyor — yani fiilen hiçbir şeyi engellemiyordu.
--
-- cardinality boş dizi için 0 döner, NULL değil; kısıt bu yüzden çalışır.
-- Değersiz bir kural hesaplamada koşulu okunamaz hâle getirir (bkz. service
-- katmanındaki matchRule) ve doğrudan SQL çalıştıran bir bakım betiği ya da
-- kısmi bir geri yükleme böyle bir satır üretebilir; kapının veri düzeyinde de
-- durması bu yüzden gerekir.
--
-- Kısıt NOT VALID DEĞİLDİR: tabloda halihazırda değersiz bir kural varsa bu
-- migration bilerek DÜŞER. Sessizce yarı uygulanmış bir kısıt, olmayan bir
-- kapıyı var sanmaktan daha kötüdür.
ALTER TABLE price_rule DROP CONSTRAINT IF EXISTS price_rule_values_check;

ALTER TABLE price_rule
    ADD CONSTRAINT price_rule_values_check CHECK (cardinality(rule_values) >= 1);
