-- promotion şemasının geri alınması (plan Bölüm 8: her up'ın down çifti olur).
--
-- Sıra bağımlılığın TERSİDİR: önce promotion/campaign'e referans veren
-- tablolar, sonra promotion, en son campaign. CASCADE kullanılmaz — bir bağın
-- gözden kaçtığını sessizce silmek yerine hata olarak görmek yeğdir.
--
-- İndeksler tablolarla birlikte düşer; ayrıca DROP edilmelerine gerek yoktur.
DROP TABLE IF EXISTS promotion_redemption;
DROP TABLE IF EXISTS promotion_rule;
DROP TABLE IF EXISTS promotion_application_method;
DROP TABLE IF EXISTS promotion;
DROP TABLE IF EXISTS campaign;
