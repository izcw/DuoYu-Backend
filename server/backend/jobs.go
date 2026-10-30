package main

import (
	"context"
	"log"
	"os/exec"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Server) jobs(ctx context.Context) {
	// A single worker holds a PostgreSQL advisory lock, including across service restarts.
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	nextCleanup := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if time.Now().After(nextCleanup) {
				nextCleanup = time.Now().Add(time.Minute)
				gcCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				if e := s.cleanupAssets(gcCtx); e != nil {
					log.Print("asset cleanup deferred or failed")
				}
				cancel()
			}
			if e := s.runJob(ctx); e != nil {
				log.Print("background job failed")
			}
		}
	}
}
func (s *Server) runJob(ctx context.Context) error {
	conn, e := s.db.Acquire(ctx)
	if e != nil {
		return e
	}
	defer conn.Release()
	var locked bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(52615261)").Scan(&locked); e != nil {
		return e
	}
	if !locked {
		return nil
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(52615261)")
	if _, e = conn.Exec(ctx, "DELETE FROM backend_sessions WHERE hash IN(SELECT hash FROM backend_sessions WHERE expires_at<now() LIMIT 200)"); e != nil {
		return e
	}
	// Interrupted jobs are explicit failures; never claim an uncertain backup succeeded.
	_, e = conn.Exec(ctx, "UPDATE backend_jobs SET status='failed',error='服务重启，任务结果未确认，请检查后重试',finished_at=now() WHERE status='running'")
	if e != nil {
		return e
	}
	var id, kind string
	e = conn.QueryRow(ctx, "UPDATE backend_jobs SET status='running' WHERE id=(SELECT id FROM backend_jobs WHERE status='queued' ORDER BY created_at LIMIT 1) RETURNING id::text,kind").Scan(&id, &kind)
	if e == pgx.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	jobctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if kind == "asset-migration" {
		e = s.migrateAssets(jobctx)
	} else {
		e = exec.CommandContext(jobctx, "sudo", "-n", "/usr/bin/systemctl", "start", "--wait", "duoyu-sync-backup.service").Run()
	}
	status, message := "succeeded", ""
	if e != nil {
		status = "failed"
		message = "任务失败，请检查服务日志、权限和磁盘空间"
	}
	_, finish := conn.Exec(context.Background(), "UPDATE backend_jobs SET status=$2,error=$3,finished_at=now() WHERE id=$1", id, status, message)
	return finish
}
