> 当前生产部署（2026-10-30）：宝塔 **网站 → Go 项目 → duoyu_backend**，绑定域名 `sync.duoyu.link`。Go 服务由宝塔启动/停止/重启并配置开机启动，监听 `127.0.0.1:5262`；旧 `duoyu-backend.service` 已停用。Go 项目运行目录 `/www/wwwroot/duoyu-backend-go`，管理端静态文件在其 `admin/`，配置文件 `/www/server/duoyu-backend-go.env`（root:duoyu-backend 640，文件赋值使用 export）。Node 同步引擎仍由 systemd 管理，监听 5260。下文早期5261/systemd部署记录仅供回滚参考。

# 多鱼业务后端与管理端

参考 DuoYu-ID：Go 1.27、net/http、pgx、PostgreSQL；管理端 Vue 3、Vue Router、Element Plus、ECharts、Vite、pnpm。管理界面沿用白底、黑色主按钮、独立编辑路由的风格。账号中心项目未修改源码，也不保存本项目业务数据。

## 入口和职责

- 正式管理入口：`https://sync.duoyu.link/admin/`。
- Go 服务监听 `127.0.0.1:5261`，提供认证、管理、文件、模板及目录 API。
- 现有 Node 同步事务引擎继续监听 `127.0.0.1:5260`。Go 验证账号与停用状态后转发 `/v1/sync/*`，保留原有幂等、三方合并、RLS 和数据代次协议。这是保留已有验证成果的模块拆分，没有将同步事务重写成另一套。
- 数据库沿用 `duoyu_extensions`；同步记录未清空。管理表使用 `backend_` 前缀，部署迁移由 PostgreSQL 管理员执行，服务角色无建表和改权限能力。

## 唯一超级管理员

仅允许 DuoYu-ID 公开账号 ID **8888888888**。部署时从可信账号库定位对应内部 `users.id`，绑定到后端环境变量 `SUPER_ADMIN_SUB`。之后使用固定 issuer + sub 检查，不信任请求中的账号 ID、昵称、邮箱或角色声明。账号中心为独立公共 SPA 客户端 `duoyu_extensions_admin` 配置限制准入，仅该账号有 grant。

管理登录使用授权码、PKCE S256、state、nonce、RS256 和在线 UserInfo。浏览器只持有 HttpOnly/Secure/SameSite=Lax 会话 Cookie；访问与续期凭证 AES-GCM 加密保存在数据库，密钥仅在服务器环境文件。每次管理请求在线复核身份，写请求校验 Origin 和 CSRF。没有新增管理员、授权其他管理员或本地密码入口。

本项目用户管理支持暂停本项目访问、设置个人文件配额、查看文件和同步历史。DuoYu-ID 的邮箱、密码、账号停用等身份管理仍在账号中心进行，本管理系统不越权修改。

## 文件

目录：`/www/data/duoyu/users/<业务用户UUID>/<SHA256>`。业务用户 UUID 沿用同步系统由 issuer + sub 生成的稳定 ID；不是可改昵称，也不直接使用外部输入作为目录名。

默认每用户 **100MiB**，管理端“存储设置”可统一调整；个人配额为空时继承默认值，个人配额 0 表示禁止新增文件。降低配额不删除已有文件。单张图片最多 20MiB，PNG/JPEG/GIF/WebP；同用户同哈希只计一次，用户之间独立计费和鉴权。账号行锁串行检查配额，避免并发上传超限。

图片内容以文件保存，数据库仅保留所有者、哈希、大小、类型及相对路径。旧 bytea 图片在文件持久化、哈希校验成功后迁移；尚未迁移的记录仍可读取。用户文件在站点根目录以外，只能通过带用户认证的 API 或超级管理员接口读取。管理发布的壁纸是明确公开资源，保存在 `catalog` 目录；未发布资源不提供公共下载。

## 用户数据模板和官方默认模板

1. 来源用户先登录扩展，编辑自己的桌面并完成云同步。
2. 用户管理点击“设为模板”，或模板管理点击“从用户创建”，选择来源用户。
3. 后端在来源账号锁内取得一致快照，包含收藏、文件夹、组件、工作区、已保存标签和同步设置；不包含账号凭证、浏览器未保存标签或内部会话。模板中的自定义图片复制到独立资源区。
4. 创建结果为草稿，审核后发布。快照内容只读；要更新来源数据，重新创建快照后切换默认模板，避免来源用户后续操作悄悄影响模板。
5. 点击“设为默认”，全局只有一个官方默认模板。尚未设置时维持原有初始化行为。新账号首次进入本扩展云端时，事务内复制模板及图片并标记已初始化；并发请求不会重复复制。已有数据的账号不覆盖。
6. 初始模板复制所有允许同步的内容；用户主动导入模板时只追加新的收藏页和工作区，不覆盖自己的壁纸、快捷组或全局设置。实体 ID 和关系重新映射，图片复制到目标用户目录并计入其配额。

用户请求使用幂等 ID，应用结果进入正常同步修订日志，其他设备照常拉取。资源目录响应不泄露来源账号或快照正文。正在使用的默认模板不能直接删除、归档或下架，应先切换或取消默认。

扩展组件商店新增“已发布组件”“页面模板”，壁纸商店新增“精选壁纸”。组件目录仅配置扩展内置 calendar/weather/note 类型，不下载执行远程 JavaScript；新增组件实现仍需发布扩展更新。

## 概览、备份和审计

概览展示真实数据库统计和最近 30 天操作/活跃用户趋势。列表以服务端每页20条查询，编辑使用独立路由，版本号避免旧表单覆盖资源更新。管理写入、模板创建、文件查看、备份下载等记录操作日志，不记录令牌或图片正文。

“立即创建备份”排入数据库任务，单工作进程使用 advisory lock 领取。后端系统用户只能通过一条固定 sudoers 命令启动现有备份 unit，不能提交任意 shell。服务代码、unit 和 sudoers 均由 root 拥有，业务服务无权修改。任务中断显示失败，不能把不确定状态标为成功。

每次备份成对生成：

- `sync-时间.dump.enc`：PostgreSQL 自定义格式备份，DYS1 / AES-256-GCM。
- `sync-时间.assets.tar.enc`：完整用户/目录图片树，DYA1 / AES-256-GCM。

数据库先取快照，随后归档只增不删的内容寻址文件，图片归档可包含额外未引用文件，避免缺失数据库所引用的文件。两者应成对保存；管理员可下载加密文件，解密密钥不提供给网页。每日03:30自动备份、保留14天；仍需另行安排异地副本。

恢复只在服务器执行，避免浏览器误触影响全部账号：

```sh
node /www/wwwroot/duoyu-sync/src/restore.mjs --backup /www/backup/duoyu-sync/sync-时间.dump.enc --dry-run
node /www/wwwroot/duoyu-sync/src/restore.mjs --backup /www/backup/duoyu-sync/sync-时间.dump.enc --confirm duoyu_extensions
```

恢复前先完整校验数据库及配对图片的 GCM 标签和归档路径；正式恢复先做新备份、停止两个业务服务、恢复数据与文件、执行增量迁移和权限脚本、清除旧管理会话、轮换同步数据代次。迁移失败维持维护状态。恢复旧版无配对图片备份时，旧 bytea 数据仍可读。不能在文件迁移后直接把公网入口绕回旧 Node 图片接口；应保留 Go 文件网关，或从一致的备份恢复。

## 配置与部署

示例见 `.env.example`。正式私密配置 `/www/server/duoyu-backend.env` 为 root 600，不提交 Git。后端目录 `/www/wwwroot/duoyu-backend`；服务 `duoyu-backend.service`。后台进程使用最小权限角色 `duoyu_backend`，不连接账号中心数据库；仅初始化部署阶段读取管理员绑定并登记 OIDC 客户端。该角色可管理本业务库各账号数据，原 `duoyu_sync_runtime` 的账号隔离策略保持。

迁移顺序：先备份 → 执行 `schema.sql` → 创建独立运行角色并执行 `grants.sql` → 配置管理员绑定/会话密钥 → 发布二进制和管理静态文件 → 启动并检查本机健康 → 调整 Nginx 到5261 → 队列迁移旧图片 → 验证成对备份。所有写入图片目录的服务必须统一使用该网关。

## 开发和测试

```powershell
pnpm --dir admin install --frozen-lockfile
pnpm --dir admin build
node scripts/admin-ui.mjs
pnpm --dir ../DuoYu-Extensions typecheck
pnpm build:admin
pnpm test:sync
pnpm --dir ../DuoYu-Extensions test:cloud:integration

cd server/backend
$env:BACKEND_TEST_DATABASE_URL='postgres://duoyu:duoyu_dev_only@127.0.0.1:55432/duoyu_sync_test'
go test -v ./...
```

Go 不在 PATH 时可使用本机临时工具链 `$env:TEMP/duoyu-go-runtime/go/bin/go.exe`。后端集成测试只接受本机55432专用测试库，每次创建隔离 schema 后删除，不能指向生产。覆盖超级管理员/普通用户边界、CSRF、旧版本更新拒绝、并发文件配额、跨用户文件隔离、旧图片迁移、用户快照、默认模板并发一次初始化及手动应用幂等。浏览器测试使用模拟身份和接口，覆盖1440px、390px、图表、从用户创建模板和设置默认；公网只检查入口和授权跳转，不代替用户登录或发邮件。

## 当前边界

- 自动同步仍由扩展打开的新标签页调度，全部关闭后下次打开继续。
- 本机文件系统是单机部署；多节点部署需共享受控文件系统或改为对象存储。
- 暂不自动删除未引用的旧文件、模板图片和历史修订；这样可支持一致备份及恢复，需后续增加带保留期和引用扫描的回收任务。当前配额统计为用户已登记文件，不包含官方共享目录。
- 本次执行备份完整性 dry-run，未对生产执行破坏性的完整恢复，也未代替8888888888账号完成真实交互登录。

## 独立项目目录（2026-10-30）

本项目已从 DuoYu-Extensions 拆分，根目录为 DuoYu/DuoYu-Backend，包含 admin、server/backend（Go API）、server/sync（同步引擎）、scripts 和 docs。依赖、构建产物、用户数据和私密配置不提交 Git。使用 pnpm 11.25.0。

宝塔中入口登记在 网站 → Go 项目 → duoyu_backend，域名 sync.duoyu.link，备注 DuoYu Backend and Admin。Go 进程由宝塔管理，Node 同步引擎仍由 duoyu-sync systemd 服务管理。静态管理端由 Go 服务提供，无需再创建第二个站点。线上运行目录保持 /www/wwwroot/duoyu-backend 与 /www/wwwroot/duoyu-sync，源代码快照位于 /www/projects/DuoYu-Backend。
