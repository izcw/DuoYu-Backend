-- Seed one current snapshot for existing users; no historical revisions are fabricated.
SELECT sync_capture_user_backup(a.id,a.head_revision)
FROM sync_accounts a
WHERE EXISTS(SELECT 1 FROM sync_entities e WHERE e.account_id=a.id)
AND NOT EXISTS(SELECT 1 FROM sync_user_backups b WHERE b.account_id=a.id);
