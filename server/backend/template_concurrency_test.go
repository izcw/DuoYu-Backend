package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Park a real request on a PostgreSQL row lock so its rival gets to run
// between the old validation query and the old write, without timing guesses.
func TestDefaultTemplateConcurrentChanges(t *testing.T) {
	for _, kind := range []string{"archive", "delete", "recapture", "quota"} {
		t.Run(kind, func(t *testing.T) {
			s := testServer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			admin := loginCookie(t, s, "trusted-super", "admin")
			var id string
			err := s.db.QueryRow(ctx, `INSERT INTO backend_catalog(kind,name,status,payload) VALUES('templates','race','published','{"format":"duoyu-template","assets":[{"bytes":2097152}]}') RETURNING id::text`).Scan(&id)
			if err != nil {
				t.Fatal(err)
			}
			method, route, body := "POST", "catalog/templates/"+id, `{"name":"race","status":"archived","version":1}`
			lock := `SELECT id FROM backend_catalog WHERE id=$1 FOR UPDATE`
			lockID := id
			switch kind {
			case "delete":
				method, route, body = "DELETE", route+"?version=1", ""
			case "recapture":
				source := Identity{accountID(s.cfg.Issuer, "capture"), "capture", "capture", ""}
				if err := s.record(ctx, source); err != nil {
					t.Fatal(err)
				}
				_, err = s.db.Exec(ctx, `INSERT INTO sync_entities(account_id,entity_type,entity_id,payload,revision) VALUES($1,'libraryItems','link','{"id":"link","kind":"link","url":"https://example.invalid/test"}',1)`, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				route, body = "templates/capture", fmt.Sprintf(`{"id":%q,"account_id":%q,"name":"race","status":"archived","version":1}`, id, source.ID)
			case "quota":
				route, body = "settings", `{"quota_mb":1}`
				lock, lockID = `SELECT id FROM backend_settings WHERE id=$1 FOR UPDATE`, "storage_quota_bytes"
			}
			guard, err := s.db.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Rollback(context.Background())
			var pid int
			if err = guard.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if _, err = guard.Exec(ctx, lock, lockID); err != nil {
				t.Fatal(err)
			}
			request := func(method, route, body string) chan int {
				result := make(chan int, 1)
				go func() { result <- callAdmin(s, admin, method, route, body, true).Code }()
				return result
			}
			// Include indirect waiters (e.g. the second request waiting for
			// the first request's transaction, which is waiting for guard).
			waiters := func() int {
				var count int
				err := s.db.QueryRow(ctx, `WITH RECURSIVE waiting(pid) AS (SELECT $1::int UNION SELECT a.pid FROM pg_stat_activity a JOIN waiting w ON w.pid=ANY(pg_blocking_pids(a.pid))) SELECT count(*)-1 FROM waiting`, pid).Scan(&count)
				if err != nil {
					t.Fatal(err)
				}
				return count
			}
			mutation := request(method, route, body)
			for waiters() < 1 {
				select {
				case code := <-mutation:
					t.Fatalf("mutation exited before barrier: %d", code)
				default:
				}
				time.Sleep(5 * time.Millisecond)
			}
			choose := request("POST", "templates/default", fmt.Sprintf(`{"id":%q}`, id))
			chosen := 0
			for chosen == 0 && waiters() < 2 {
				select {
				case chosen = <-choose:
				default:
					time.Sleep(5 * time.Millisecond)
				}
			}
			if err = guard.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if chosen == 0 {
				chosen = <-choose
			}
			changed := <-mutation
			var valid bool
			err = s.db.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM backend_settings d LEFT JOIN backend_catalog c ON c.id::text=d.value#>>'{}' WHERE d.id='default_template_id' AND d.value#>>'{}'<>'' AND (c.id IS NULL OR c.status<>'published' OR (SELECT coalesce(sum((asset->>'bytes')::bigint),0) FROM jsonb_array_elements(c.payload->'assets') asset)>(SELECT (value#>>'{}')::bigint FROM backend_settings WHERE id='storage_quota_bytes')))`).Scan(&valid)
			if err != nil {
				t.Fatal(err)
			}
			if !valid || !(changed == 200 && (chosen == 409 || chosen == 400) || chosen == 200 && changed == 409) {
				t.Fatalf("invalid final state or conflicting responses: valid=%v mutation=%d selection=%d", valid, changed, chosen)
			}
		})
	}
}
