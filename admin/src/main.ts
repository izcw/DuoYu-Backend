import { createApp } from 'vue';
import { createRouter, createWebHistory } from 'vue-router';
import ElementPlus from 'element-plus';
import zhCn from 'element-plus/es/locale/lang/zh-cn';
import 'element-plus/dist/index.css';
import App from './App.vue';
const Dashboard = () => import('./Dashboard.vue');
const Manage = () => import('./Manage.vue');
import './style.css';
const router = createRouter({
  history: createWebHistory('/admin/'),
  routes: [
    { path: '/', component: Dashboard },
    { path: '/:section/:id?/:tab?', component: Manage },
  ],
});
createApp(App).use(router).use(ElementPlus, { locale: zhCn }).mount('#app');
