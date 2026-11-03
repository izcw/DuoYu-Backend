export const menus = [
  { path: '/', title: '概览', icon: 'ri:dashboard-line' },
  { path: '/users', title: '用户管理', icon: 'ri:group-line' },
  { path: '/templates', title: '模板管理', icon: 'ri:file-copy-line' },
  { path: '/widgets', title: '组件管理', icon: 'ri:function-line' },
  { path: '/wallpapers', title: '壁纸管理', icon: 'ri:image-line' },
  { path: '/backups', title: '同步与备份', icon: 'ri:database-2-line' },
  { path: '/settings', title: '存储设置', icon: 'ri:settings-3-line' },
  { path: '/audit', title: '操作记录', icon: 'ri:file-list-3-line' },
];
export const menuGroups = [
  { title: '工作台', items: menus.slice(0, 2) },
  { title: '内容管理', items: menus.slice(2, 5) },
  { title: '系统管理', items: menus.slice(5) },
];
export const pageName = (section: unknown) =>
  menus.find((m) => m.path === '/' + (section || ''))?.title || '概览';
