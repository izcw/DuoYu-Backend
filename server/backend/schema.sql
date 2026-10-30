BEGIN;
ALTER TABLE sync_assets ALTER COLUMN body DROP NOT NULL;
ALTER TABLE sync_assets ADD COLUMN IF NOT EXISTS byte_size bigint;
ALTER TABLE sync_assets ADD COLUMN IF NOT EXISTS file_path text;
UPDATE sync_assets SET byte_size=octet_length(body) WHERE byte_size IS NULL;
ALTER TABLE sync_assets ALTER COLUMN byte_size SET NOT NULL;
ALTER TABLE sync_assets ALTER COLUMN byte_size SET DEFAULT 0;
CREATE TABLE IF NOT EXISTS backend_settings(id text PRIMARY KEY,value jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS backend_catalog_files(sha256 text PRIMARY KEY,media_type text NOT NULL,byte_size bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT now());
INSERT INTO backend_settings VALUES('storage_quota_bytes','104857600') ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS backend_users(account_id uuid PRIMARY KEY REFERENCES sync_accounts(id),name text NOT NULL DEFAULT '',disabled boolean NOT NULL DEFAULT false,quota_bytes bigint CHECK(quota_bytes IS NULL OR quota_bytes>=0),last_seen timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS backend_catalog(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),kind text NOT NULL CHECK(kind IN ('templates','widgets','wallpapers')),name text NOT NULL,description text NOT NULL DEFAULT '',status text NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','published','archived')),payload jsonb NOT NULL DEFAULT '{}',version bigint NOT NULL DEFAULT 1,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS backend_audit(id bigserial PRIMARY KEY,actor text NOT NULL,action text NOT NULL,target text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS backend_sessions(hash text PRIMARY KEY,encrypted bytea NOT NULL,expires_at timestamptz NOT NULL);
CREATE TABLE IF NOT EXISTS backend_jobs(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),kind text NOT NULL CHECK(kind IN ('backup','asset-migration')),status text NOT NULL DEFAULT 'queued',error text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),finished_at timestamptz);
ALTER TABLE backend_users ADD COLUMN IF NOT EXISTS initialized boolean NOT NULL DEFAULT false;
-- Existing project accounts, including intentionally empty ones, are not new registrations.
INSERT INTO backend_users(account_id,initialized) SELECT id,true FROM sync_accounts ON CONFLICT(account_id) DO NOTHING;
CREATE TABLE IF NOT EXISTS backend_template_applies(account_id uuid NOT NULL REFERENCES sync_accounts(id),request_id uuid NOT NULL,template_id uuid NOT NULL,revision bigint NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(account_id,request_id));
CREATE OR REPLACE FUNCTION sync_asset_size() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.body IS NOT NULL THEN NEW.byte_size=octet_length(NEW.body); END IF; RETURN NEW; END $$;
DROP TRIGGER IF EXISTS sync_asset_size_trigger ON sync_assets;
CREATE TRIGGER sync_asset_size_trigger BEFORE INSERT OR UPDATE ON sync_assets FOR EACH ROW EXECUTE FUNCTION sync_asset_size();
COMMIT;

ALTER TABLE backend_users ADD COLUMN IF NOT EXISTS picture text NOT NULL DEFAULT '';
