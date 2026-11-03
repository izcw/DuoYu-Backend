# 用户备份恢复

GET /api/v1/sync/backups 返回当前认证账号最近五份快照的元信息。POST /api/v1/sync/backups/:id/restore 接收 id（幂等请求 UUID）、deviceId、epoch 和 revision（用户确认时的当前云端版本）。账号由认证提供，客户端不能指定其他账号。

恢复复用同步事务、账号行锁、实体图验证、图片引用检查、回执和快照机制。云端版本发生变化时返回 backup_revision_changed，不覆盖新修改。图片不可读取或校验失败时整个事务回滚；旧快照恢复为新的同步版本，不修改全局 epoch。

同步进程需配置与图片服务一致的 ASSET_ROOT，并有只读权限，用于恢复备份仍保留但已移除数据库引用的图片。无法读取时拒绝恢复。历史内嵌图片会校验 SHA-256。发布需同步更新 Node 同步服务和扩展；数据库需已有 sync_user_backups 及既有权限脚本。

隔离测试：设置 DUOYU_RESTORE_TEST_DATABASE_URL 指向临时 PostgreSQL，运行 node --test test/backup-restore.test.mjs。不得使用生产数据库。
