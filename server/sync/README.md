# DuoYu 同步服务

运行要求：Node.js 24、PostgreSQL。完整功能和生产交付见 ../../docs/cloud-sync-delivery.md。

安装：`npm ci --omit=dev --ignore-scripts`。管理员执行schema.sql迁移后，为运行角色只授予public schema USAGE及同步表的SELECT/INSERT/UPDATE/DELETE。运行角色不得拥有表、超级用户、CREATEDB、CREATEROLE或BYPASSRLS权限。不要直接使用初始化数据库账号运行服务。

通过服务端环境配置PGHOST、PGPORT、PGDATABASE、PGUSER、PGPASSWORD、PGSSLMODE、DUOYU_ISSUER、DUOYU_CLIENT_ID、SYNC_ORIGINS及PORT。不得添加VITE前缀或把数据库密码复制进扩展配置。

`node src/main.mjs`启动，默认监听127.0.0.1:5260，仅供Go业务网关内部转发。公开Nginx入口必须转发至Go，不能直连Node绕过项目停权、文件和管理路由。本目录Nginx模板按根README记录的宝塔Go端口5262配置；旧systemd示例使用5261，部署时必须与实际LISTEN一致。systemd模板及私密环境文件由运维核对后配置。`node src/main.mjs --migrate`仅供具备迁移权限的部署环境，不能作为运行账号每次启动时的命令。

收到 SIGTERM/SIGINT 后停止接收新连接，等待已接收请求完成，再关闭数据库连接池；重复信号共用同一关闭过程。HTTP 排空宽限期为30秒，超时则关闭剩余连接并报告非正常停机，不能据此认定所有请求已提交。部署进程管理器还应保留数据库连接释放的时间；本地测试不替代实际部署环境的停机演练。

自动化测试：`npm test`，使用本机独立的duoyu_sync_test数据库。默认本地开发容器端口55432，不连接生产数据库，不发送邮件。

完整恢复与 RLS 测试还需显式设置 `DUOYU_RESTORE_TEST_DATABASE_URL` 为本机专用测试库；未设置时对应测试会跳过。归档测试验证加密完整性、同名及并发创建不会删除已有备份、tar 启动失败清理与重试、上传临时文件排除、目录白名单及硬链接拒绝。HTTP 边界测试通过实际本机请求覆盖非法变更元素、JSON 错误以及固定长度/分块传输的请求体上限；非法输入返回 400，超限返回 413。

账号云端备份恢复在单个事务内执行。普通同步上传每批最多 2,000 条变更；服务端从已鉴权备份生成的恢复操作可覆盖完整账号，仍遵守 10,000 条记录（包含删除标记）和 8 MiB 的既有账号配额，不开放客户端上限参数。`large-restore.test.mjs` 使用专用测试库验证 2,001 条恢复、10,000 条边界、失败回滚、幂等重试、版本冲突及账号隔离。

备份：src/backup.mjs仅在Linux服务器以管理员权限运行，使用root可读的32字节备份密钥，AES-256-GCM加密pg_dump自定义格式。`--verify <备份路径>`验证完整性，不导出明文。恢复需先在隔离目录用同一密钥解密并验证GCM tag，再将已验证的pg_dump内容交给pg_restore；不要把未经完整性验证的流直接输入生产数据库。正式恢复前停止同步写入、备份现状，并规划客户端cursor重建。
