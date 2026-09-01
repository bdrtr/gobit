-- b2b şemasının geri alınması. Sıra, foreign key bağımlılıklarının tersidir:
-- önce çalışan tablosu düşer, sonra şirket.
--
-- Link tablosu ("link_b2b_employee_customer") BURADA DÜŞÜRÜLMEZ ve bu
-- bilinçlidir: link şeması migration'ın değil, açılıştaki bildirimin
-- ürünüdür (ADR 0005) ve sahibi core/link'tir. Bu dosyanın onu düşürmesi,
-- bir modülün migration'ının başka bir alt sistemin tablosunu silmesi
-- olurdu — ve b2b geri alındıktan sonra bile o tablodaki satırlar zararsızdır,
-- çünkü işaret ettikleri çalışan kimlikleri bir daha üretilmez.
DROP INDEX IF EXISTS b2b_company_employee_company_idx;
DROP TABLE IF EXISTS b2b_company_employee;

DROP INDEX IF EXISTS b2b_company_email_idx;
DROP TABLE IF EXISTS b2b_company;
