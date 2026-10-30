-- Run as the migration owner. The service role cannot change schema or policies.
GRANT CONNECT ON DATABASE duoyu_extensions TO duoyu_backend;
GRANT USAGE ON SCHEMA public TO duoyu_backend;
GRANT SELECT,INSERT,UPDATE ON sync_accounts,sync_entities,sync_changes,sync_assets TO duoyu_backend;
GRANT SELECT ON sync_conflicts,sync_receipts,sync_generation TO duoyu_backend;
GRANT SELECT,INSERT,UPDATE,DELETE ON backend_users,backend_catalog,backend_sessions,backend_jobs,backend_template_applies TO duoyu_backend;
GRANT SELECT,INSERT,UPDATE ON backend_settings TO duoyu_backend;
GRANT SELECT,INSERT ON backend_catalog_files,backend_audit TO duoyu_backend;
GRANT USAGE,SELECT ON SEQUENCE backend_audit_id_seq TO duoyu_backend;
DO $$ DECLARE relation text; BEGIN
 FOREACH relation IN ARRAY ARRAY['sync_accounts','sync_entities','sync_changes','sync_assets','sync_conflicts','sync_receipts'] LOOP
  IF NOT EXISTS(SELECT 1 FROM pg_policies WHERE schemaname='public' AND tablename=relation AND policyname='backend_management') THEN
   EXECUTE format('CREATE POLICY backend_management ON %I TO duoyu_backend USING(true) WITH CHECK(true)',relation);
  END IF;
 END LOOP;
END $$;
