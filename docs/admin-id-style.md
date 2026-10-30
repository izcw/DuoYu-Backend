# 管理端与 DuoYu-ID 样式对齐（2026-10-30）

管理端采用 DuoYu-ID 的白色侧栏、浅灰工作区、黑白主色、60px 顶栏、230px/64px 可折叠菜单、面包屑与页面页签。统计卡片、页面标题、表格、表单、按钮、间距直接使用 DuoYu-ID 的 dy-* 公共 CSS，不再另写同名样式。

## 维护方式

`admin/src/vendor/duoyu-id/duoyu.css` 是原文件的完整副本；来源及 SHA-256 记录在同目录 source.json。不要手动修改或格式化这份文件。

```powershell
pnpm --dir admin sync:id-style
pnpm --dir admin build
node scripts/admin-ui.mjs
```

同步脚本默认读取旁边的 DuoYu-ID 项目，也可以用 DUOYU_ID_DIR 指定路径。它同时同步默认头像。业务布局与响应式外壳放在 admin/src/style.css。Remix 图标与 DuoYu-ID 一样使用离线 Iconify 集合，构建时按源码提取，不依赖线上图标接口。

## 验证结果

- Vue 类型检查及生产构建通过。
- Edge 自动化通过：桌面概览、图表、230px/64px 菜单折叠、离线 SVG 图标、390px 手机菜单与页面无横向溢出。
- 用户快照创建模板、设置官方默认模板通过模拟 API 检查；未保存表单点击顶栏刷新后取消，输入内容保留。
- 未发现浏览器脚本异常。
- 已部署静态管理端到 https://sync.duoyu.link/admin/，发布前保留原静态文件备份。生产入口、默认头像返回 200，未登录管理 API 返回 401，登录正确跳转 DuoYu ID。
- 本轮没有使用真实管理员登录执行数据写入，业务写入流程通过本地模拟 API 验证；没有修改数据库和后端服务。
