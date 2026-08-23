-- Yukarıdaki şemayı tümüyle geri alır (plan Bölüm 8: up/down çiftleri).
--
-- Sıra önemlidir: adımlar tablosu yürütmelere FK ile bağlıdır, önce o düşer.
-- İndeksler tabloyla birlikte kendiliğinden düşer; yine de açıkça yazılmıştır
-- ki tablo elle korunmak istendiğinde tek tek geri alınabilsinler.
DROP TABLE IF EXISTS workflow_execution_steps;

DROP INDEX IF EXISTS workflow_executions_workflow_created_at_idx;
DROP INDEX IF EXISTS workflow_executions_idempotency_key_uniq;

DROP TABLE IF EXISTS workflow_executions;
