export const menus = [
  { path: '/', title: '概览', icon: 'ri:dashboard-line' },
  { path: '/users', title: '用户管理', icon: 'ri:group-line' },
  { path: '/templates', title: '模板管理', icon: 'ri:file-copy-line' },
  { path: '/widgets', title: '组件管理', icon: 'ri:function-line' },
  { path: '/wallpapers', title: '壁纸管理', icon: 'ri:image-line' },
  { path: '/backups', title: '备份管理', icon: 'ri:database-2-line' },
  { path: '/sync', title: '同步管理', icon: 'ri:refresh-line' },
  { path: '/settings', title: '存储设置', icon: 'ri:settings-3-line' },
  { path: '/audit', title: '操作记录', icon: 'ri:file-list-3-line' },
];
export const pageName = (section: unknown) =>
  menus.find((m) => m.path === '/' + (section || ''))?.title || '概览';
