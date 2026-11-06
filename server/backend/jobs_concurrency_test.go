package main

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"
)

func TestJobAdmission(t *testing.T) {
	s := testServer(t)
	admin := loginCookie(t, s, "trusted-super", "admin")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	enqueue := func(kind string) int {
		return callAdmin(s, admin, "POST", "jobs", fmt.Sprintf(`{"kind":%q}`, kind), true).Code
	}
	// The isolated server has no running worker. A trigger holds the first
	// insert open so that both requests reach the database before commit.
	guard, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Rollback(context.Background())
	var pid int
	if err = guard.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = guard.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", int64(pid)); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(ctx, fmt.Sprintf(`CREATE FUNCTION pause_job() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.kind='backup' THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END$$; CREATE TRIGGER pause_job BEFORE INSERT ON backend_jobs FOR EACH ROW EXECUTE FUNCTION pause_job()`, pid))
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() { result <- enqueue("backup") }()
	}
	for {
		var waiting int
		if err = s.db.QueryRow(ctx, `WITH RECURSIVE waiting(pid) AS (SELECT $1::int UNION SELECT a.pid FROM pg_stat_activity a JOIN waiting w ON w.pid=ANY(pg_blocking_pids(a.pid))) SELECT count(*)-1 FROM waiting`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code := enqueue("asset-migration"); code != 202 {
		t.Fatalf("independent kind: %d", code)
	}
	if err = guard.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	status := []int{<-result, <-result}
	sort.Ints(status)
	var count int
	if err = s.db.QueryRow(ctx, "SELECT count(*) FROM backend_jobs WHERE kind='backup'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || status[0] != 202 || status[1] != 409 {
		t.Fatalf("duplicate concurrent admissions: statuses=%v rows=%d", status, count)
	}
	for _, state := range []string{"running", "succeeded"} {
		if _, err = s.db.Exec(ctx, "UPDATE backend_jobs SET status=$1 WHERE kind='backup'", state); err != nil {
			t.Fatal(err)
		}
		expected := 409
		if state == "succeeded" {
			expected = 202
		}
		if code := enqueue("backup"); code != expected {
			t.Fatalf("%s: status %d", state, code)
		}
	}
}

func TestJobAdmissionDatabaseFailure(t *testing.T) {
	s := testServer(t)
	admin := loginCookie(t, s, "trusted-super", "admin")
	ctx := context.Background()
	_, err := s.db.Exec(ctx, `CREATE FUNCTION reject_job() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture failure'; END$$; CREATE TRIGGER reject_job BEFORE INSERT ON backend_jobs FOR EACH ROW EXECUTE FUNCTION reject_job()`)
	if err != nil {
		t.Fatal(err)
	}
	call := func() int { return callAdmin(s, admin, "POST", "jobs", `{"kind":"backup-full"}`, true).Code }
	if code := call(); code != 500 {
		t.Fatalf("database failure misreported as duplicate: %d", code)
	}
	if _, err = s.db.Exec(ctx, "DROP TRIGGER reject_job ON backend_jobs"); err != nil {
		t.Fatal(err)
	}
	if code := call(); code != 202 {
		t.Fatalf("retry after failure: %d", code)
	}
}
