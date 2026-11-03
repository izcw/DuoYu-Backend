BEGIN;
CREATE TABLE IF NOT EXISTS sync_generation(singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),epoch uuid NOT NULL DEFAULT gen_random_uuid());
INSERT INTO sync_generation(singleton) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS sync_accounts(id uuid PRIMARY KEY,issuer text NOT NULL,subject text NOT NULL,head_revision bigint NOT NULL DEFAULT 0,UNIQUE(issuer,subject));
CREATE TABLE IF NOT EXISTS sync_entities(account_id uuid NOT NULL REFERENCES sync_accounts(id),entity_type text NOT NULL,entity_id text NOT NULL,payload jsonb NOT NULL,deleted boolean NOT NULL DEFAULT false,revision bigint NOT NULL,PRIMARY KEY(account_id,entity_type,entity_id));
CREATE TABLE IF NOT EXISTS sync_changes(account_id uuid NOT NULL REFERENCES sync_accounts(id),revision bigint NOT NULL,operation_id uuid NOT NULL,changes jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(account_id,revision));
CREATE TABLE IF NOT EXISTS sync_receipts(account_id uuid NOT NULL REFERENCES sync_accounts(id),operation_id uuid NOT NULL,request_hash text NOT NULL,result jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(account_id,operation_id));
CREATE TABLE IF NOT EXISTS sync_conflicts(id uuid PRIMARY KEY,account_id uuid NOT NULL REFERENCES sync_accounts(id),operation_id uuid NOT NULL,detail jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE sync_conflicts ADD COLUMN IF NOT EXISTS resolved_at timestamptz;
CREATE UNIQUE INDEX IF NOT EXISTS sync_conflict_operation ON sync_conflicts(account_id,operation_id);
CREATE TABLE IF NOT EXISTS sync_assets(account_id uuid NOT NULL REFERENCES sync_accounts(id),sha256 text NOT NULL,media_type text NOT NULL,body bytea NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(account_id,sha256));
CREATE INDEX IF NOT EXISTS sync_entity_revision ON sync_entities(account_id,revision);
DO $$
DECLARE relation text;
BEGIN
 FOREACH relation IN ARRAY ARRAY['sync_accounts','sync_entities','sync_changes','sync_receipts','sync_conflicts','sync_assets'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',relation);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',relation);
  IF NOT EXISTS(SELECT 1 FROM pg_policies WHERE schemaname='public' AND tablename=relation AND policyname='account_isolation') THEN
   EXECUTE format('CREATE POLICY account_isolation ON %I USING (%I = nullif(current_setting(''app.account_id'', true), '''')::uuid) WITH CHECK (%I = nullif(current_setting(''app.account_id'', true), '''')::uuid)',relation,CASE WHEN relation='sync_accounts' THEN 'id' ELSE 'account_id' END,CASE WHEN relation='sync_accounts' THEN 'id' ELSE 'account_id' END);
  END IF;
 END LOOP;
END $$;
COMMIT;

-- Asset lifecycle: an upload lease protects the gap between file upload and data commit.
ALTER TABLE sync_assets ADD COLUMN IF NOT EXISTS referenced_at timestamptz;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='sync_assets' AND column_name='last_uploaded_at') THEN
 ALTER TABLE sync_assets ADD COLUMN last_uploaded_at timestamptz NOT NULL DEFAULT now();
 UPDATE sync_assets SET last_uploaded_at=created_at;
 END IF;
END $$;
CREATE TABLE IF NOT EXISTS sync_asset_deletions(account_id uuid NOT NULL REFERENCES sync_accounts(id),sha256 text NOT NULL,requested_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(account_id,sha256));
ALTER TABLE sync_asset_deletions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_asset_deletions FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_policies WHERE schemaname=current_schema() AND tablename='sync_asset_deletions' AND policyname='account_isolation') THEN
 CREATE POLICY account_isolation ON sync_asset_deletions USING(account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK(account_id=nullif(current_setting('app.account_id',true),'')::uuid);
 END IF;
END $$;
CREATE OR REPLACE FUNCTION sync_asset_references(v jsonb) RETURNS SETOF text LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE child jsonb; t text; icon jsonb; hit text[];
BEGIN
 IF jsonb_typeof(v)='string' THEN
  t=v#>>'{}';
  IF left(t,11)='duoyu-icon:' THEN
   BEGIN icon=substring(t from 12)::jsonb; EXCEPTION WHEN others THEN RETURN; END;
   IF icon->>'mode'='upload' THEN RETURN QUERY SELECT sync_asset_references(icon->'image'); END IF;
   RETURN;
  END IF;
  FOR hit IN SELECT regexp_matches(t,'duoyu-asset:([a-f0-9]{64})','g') LOOP RETURN NEXT hit[1]; END LOOP;
 ELSIF jsonb_typeof(v)='object' THEN
  FOR child IN SELECT value FROM jsonb_each(v) LOOP RETURN QUERY SELECT sync_asset_references(child); END LOOP;
 ELSIF jsonb_typeof(v)='array' THEN
  FOR child IN SELECT value FROM jsonb_array_elements(v) LOOP RETURN QUERY SELECT sync_asset_references(child); END LOOP;
 END IF;
END $$;
CREATE OR REPLACE FUNCTION sync_reconcile_assets(owner_id uuid,expire_uploads boolean DEFAULT false) RETURNS integer LANGUAGE plpgsql AS $$
DECLARE refs text[]; removed integer;
BEGIN
 PERFORM id FROM sync_accounts WHERE id=owner_id FOR UPDATE;
 SELECT coalesce(array_agg(DISTINCT h),'{}'::text[]) INTO refs FROM (
  SELECT sync_asset_references(payload) h FROM sync_entities WHERE account_id=owner_id AND NOT deleted
  UNION SELECT sync_asset_references(detail) h FROM sync_conflicts WHERE account_id=owner_id AND resolved_at IS NULL
 ) r;
 UPDATE sync_assets SET referenced_at=now() WHERE account_id=owner_id AND sha256=ANY(refs);
 WITH expired AS (
  DELETE FROM sync_assets WHERE account_id=owner_id AND NOT(sha256=ANY(refs)) AND
   ((referenced_at IS NOT NULL AND last_uploaded_at<now()-interval '10 minutes') OR
    (expire_uploads AND last_uploaded_at<now()-interval '24 hours'))
  RETURNING account_id,sha256
 ) INSERT INTO sync_asset_deletions(account_id,sha256) SELECT account_id,sha256 FROM expired ON CONFLICT DO NOTHING;
 GET DIAGNOSTICS removed=ROW_COUNT;
 RETURN removed;
END $$;

-- The snapshot and the accepted cloud revision commit or roll back together.
CREATE TABLE IF NOT EXISTS sync_user_backups(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES sync_accounts(id),
 epoch uuid NOT NULL, revision bigint NOT NULL,
 snapshot jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(account_id,epoch,revision)
);
CREATE INDEX IF NOT EXISTS sync_user_backups_recent ON sync_user_backups(account_id,created_at DESC);
ALTER TABLE sync_user_backups ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_user_backups FORCE ROW LEVEL SECURITY;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_policies WHERE schemaname=current_schema() AND tablename='sync_user_backups' AND policyname='account_isolation') THEN
 CREATE POLICY account_isolation ON sync_user_backups USING(account_id=nullif(current_setting('app.account_id',true),'')::uuid) WITH CHECK(account_id=nullif(current_setting('app.account_id',true),'')::uuid);
 END IF;
END $$;
CREATE OR REPLACE FUNCTION sync_capture_user_backup(owner_id uuid,rev bigint) RETURNS void LANGUAGE plpgsql AS $$
DECLARE entities jsonb; assets jsonb; generation uuid;
BEGIN
 PERFORM id FROM sync_accounts WHERE id=owner_id FOR UPDATE;
 SELECT epoch INTO generation FROM sync_generation WHERE singleton;
 SELECT coalesce(jsonb_agg(jsonb_build_object('table',entity_type,'id',entity_id,'payload',payload,'deleted',deleted,'revision',revision::text) ORDER BY entity_type,entity_id),'[]'::jsonb) INTO entities FROM sync_entities WHERE account_id=owner_id;
 SELECT coalesce(jsonb_object_agg(sha256,jsonb_build_object('media_type',media_type,'bytes',coalesce((to_jsonb(a)->>'byte_size')::bigint,octet_length(body),0),'body',CASE WHEN body IS NOT NULL THEN encode(body,'base64') ELSE NULL END)),'{}'::jsonb) INTO assets FROM sync_assets a
 WHERE account_id=owner_id AND sha256 IN(SELECT sync_asset_references(payload) FROM sync_entities WHERE account_id=owner_id AND NOT deleted);
 INSERT INTO sync_user_backups(account_id,epoch,revision,snapshot) VALUES(owner_id,generation,rev,jsonb_build_object('format','duoyu-user-snapshot','version',1,'account_id',owner_id,'epoch',generation,'revision',rev::text,'entities',entities,'assets',assets)) ON CONFLICT DO NOTHING;
 DELETE FROM sync_user_backups WHERE account_id=owner_id AND id NOT IN(SELECT id FROM sync_user_backups WHERE account_id=owner_id ORDER BY created_at DESC,id DESC LIMIT 5);
END $$;
CREATE OR REPLACE FUNCTION sync_backup_on_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 PERFORM sync_capture_user_backup(NEW.account_id,NEW.revision); RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS sync_user_backup_trigger ON sync_changes;
CREATE TRIGGER sync_user_backup_trigger AFTER INSERT ON sync_changes FOR EACH ROW EXECUTE FUNCTION sync_backup_on_change();
CREATE OR REPLACE FUNCTION sync_backup_has_asset(owner_id uuid,hash text) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM sync_user_backups WHERE account_id=owner_id AND snapshot->'assets' ? hash)
$$;
