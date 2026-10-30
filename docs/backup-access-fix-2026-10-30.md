# 备份管理不可用修复（2026-10-30）

原因：/www/backup 的目录模式为0600，后端无法穿越该父目录；宝塔Go项目实际进程的Groups列表为空，没有继承Unix账号的duoyu-backup附加组，所以仅给子目录750、备份文件640和配置用户组仍不足以读取。实际日志为GET /api/admin/backups失败。

修复：为duoyu-backend的UID增加精确POSIX ACL，父目录只允许穿越、不允许列举；备份目录只允许读取/穿越，不允许写入；只读访问符合命名规则的加密备份。保留其他现有ACL，不开放给所有用户。目录默认ACL保证后续新备份在生成完成并chmod0640后可读，生成时0600仍被权限掩码保护。备份服务ExecStartPre运行scripts/provision-backup-access.py，持续修复权限漂移；服务器helper位于/www/server/duoyu-backup-access.py，root拥有、0700。

代码同时改进：自动过期清理与列表并发时，跳过已消失的文件，避免整个列表500；列表和下载不跟随符号链接；下载固定已打开文件句柄并使用private,no-store；无法读取目录/文件时返回明确503提示。

验证：使用setpriv清除所有附加组，以实际后端UID/GID读取现有和新生成的全部加密备份，确认无备份目录写权限。真实备份任务Result=success、ExecMainStatus=0。Go专用本地测试库回归11项通过，新增备份列表过滤、排序、分页、管理员下载、审计、普通用户拒绝、文件名/目录/符号链接保护、目录不可读诊断。go vet通过，线上Go项目与登录入口健康。未代替管理员交互登录生产系统。

本机原测试数据库端口未启动，因此新建了隔离测试集群DuoYu-Backend/work/backup-fix-pg/data，监听127.0.0.1:55432，仅使用duoyu_sync_test专用库，无生产连接。
