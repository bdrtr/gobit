-- Forgetting every invitation that had not been accepted yet.
--
-- No account is lost: an invitation is the right to set a first password and
-- nothing more. Everybody holding an unused link has to be invited again.
DROP INDEX IF EXISTS auth_user_invitation_expires_idx;
DROP INDEX IF EXISTS auth_user_invitation_user_uniq;
DROP TABLE IF EXISTS auth_user_invitation;
