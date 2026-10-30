<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessage } from "element-plus";
import { session, loadSession } from "./api";
import AdminIcon from "./AdminIcon.vue";
import UserAvatar from "./UserAvatar.vue";
import { menus, pageName } from "./navigation";
const route = useRoute(),
  router = useRouter(),
  mobile = ref(matchMedia("(max-width:800px)").matches),
  menuOpen = ref(false),
  collapsed = ref(false);
try {
  collapsed.value =
    localStorage.getItem("duoyu-admin-menu-collapsed") === "true";
} catch {}
const activeMenu = computed(() => "/" + (route.params.section || ""));
const detailName = computed(() =>
  route.params.id === "new"
    ? "新建"
    : route.params.tab === "settings"
      ? "访问设置"
      : route.params.id
        ? "详情"
        : "",
);
const title = computed(
  () =>
    pageName(route.params.section) +
    (detailName.value ? " · " + detailName.value : ""),
);
const tabs = ref([{ path: "/", to: "/", title: "概览" }]);
watch(
  () => route.fullPath,
  () => {
    menuOpen.value = false;
    const item = tabs.value.find((t) => t.path === route.path);
    if (item) {
      item.to = route.fullPath;
      item.title = title.value;
    } else
      tabs.value.push({
        path: route.path,
        to: route.fullPath,
        title: title.value,
      });
  },
  { immediate: true },
);
function resized() {
  const next = matchMedia("(max-width:800px)").matches;
  if (next !== mobile.value) menuOpen.value = false;
  mobile.value = next;
}
function toggleMenu() {
  if (mobile.value) menuOpen.value = !menuOpen.value;
  else {
    collapsed.value = !collapsed.value;
    try {
      localStorage.setItem(
        "duoyu-admin-menu-collapsed",
        String(collapsed.value),
      );
    } catch {}
  }
}
async function navigate(path: string) {
  await router.push(path);
  menuOpen.value = false;
}
async function closeTab(path: string) {
  if (path === "/") return;
  if (route.path === path) {
    const i = tabs.value.findIndex((t) => t.path === path),
      next = tabs.value[i - 1] || tabs.value[0];
    await router.push(next.to);
    if (route.path !== next.path) return;
  }
  tabs.value = tabs.value.filter((t) => t.path !== path);
}
function refresh() {
  window.dispatchEvent(new Event("duoyu-admin-refresh"));
}
onMounted(() => {
  void loadSession();
  window.addEventListener("resize", resized);
});
onUnmounted(() => window.removeEventListener("resize", resized));
async function command(value: string) {
  if (value === "profile") {
    window.open(
      "https://id.duoyu.link/#/account/profile",
      "_blank",
      "noopener",
    );
    return;
  }
  try {
    const r = await fetch("/auth/logout", {
      method: "POST",
      headers: { "X-CSRF-Token": session.csrf },
    });
    if (!r.ok) throw Error("退出失败");
    session.authenticated = false;
  } catch (e) {
    ElMessage.error(String(e));
  }
}
</script>
<template>
  <div v-if="!session.ready" class="admin-login" role="status">
    正在连接管理系统…
  </div>
  <div v-else-if="!session.authenticated" class="admin-login">
    <img :src="'/admin/logo.png'" alt="多鱼" />
    <h1>多鱼管理系统</h1>
    <p>使用多鱼账号登录</p>
    <a class="admin-login-button" href="/auth/login">登录 DuoYu ID</a>
    <p class="dy-muted">仅账号 8888888888 可访问</p>
    <p v-if="session.error" class="dy-muted" role="alert">
      {{ session.error }}
    </p>
    <el-button text @click="loadSession">重试连接</el-button>
  </div>
  <div
    v-else
    class="app-layout"
    :class="{
      'menu-collapsed': collapsed && !mobile,
      'mobile-menu-open': mobile && menuOpen,
    }"
  >
    <button
      v-if="mobile && menuOpen"
      class="menu-scrim"
      aria-label="关闭菜单"
      @click="menuOpen = false"
    />
    <aside
      id="app-sidebar"
      class="layout-sidebar"
      :class="{ visible: mobile && menuOpen }"
      aria-label="管理菜单"
    >
      <RouterLink class="sidebar-brand" to="/" aria-label="多鱼管理概览"
        ><img :src="'/admin/logo.png'" alt="" /><span
          >多鱼管理</span
        ></RouterLink
      >
      <el-scrollbar class="sidebar-scroll"
        ><el-menu
          class="el-menu-design"
          :default-active="activeMenu"
          :collapse="collapsed && !mobile"
          :collapse-transition="false"
          @select="navigate"
          ><el-menu-item
            v-for="item in menus"
            :key="item.path"
            :index="item.path"
            ><AdminIcon :name="item.icon" /><template #title
              ><span>{{ item.title }}</span></template
            ></el-menu-item
          ></el-menu
        ></el-scrollbar
      >
    </aside>
    <main id="app-main">
      <div id="app-header">
        <div class="header-bar">
          <div class="header-left">
            <button
              class="header-icon-button"
              :aria-label="
                mobile ? '打开菜单' : collapsed ? '展开菜单' : '收起菜单'
              "
              @click="toggleMenu"
            >
              <AdminIcon name="ri:menu-2-fill" /></button
            ><button
              class="header-icon-button header-refresh"
              aria-label="刷新当前页面"
              @click="refresh"
            >
              <AdminIcon name="ri:refresh-line" /></button
            ><el-breadcrumb separator="/" class="header-breadcrumb"
              ><el-breadcrumb-item :to="'/'">首页</el-breadcrumb-item
              ><el-breadcrumb-item
                v-if="route.params.section"
                :to="'/' + route.params.section"
                >{{ pageName(route.params.section) }}</el-breadcrumb-item
              ><el-breadcrumb-item v-if="detailName">{{
                detailName
              }}</el-breadcrumb-item></el-breadcrumb
            >
          </div>
          <div class="dy-header-actions">
            <el-dropdown @command="command" trigger="click"
              ><button class="dy-header-user" aria-label="管理员账号菜单">
                <UserAvatar
                  :src="session.profile.picture"
                  :name="session.profile.name"
                  :size="34"
                /><span class="dy-header-username" title="8888888888"
                  >8888888888</span
                ><span class="dy-header-admin-badge">超级管理员</span></button
              ><template #dropdown
                ><el-dropdown-menu
                  ><el-dropdown-item command="profile"
                    >账号与安全</el-dropdown-item
                  ><el-dropdown-item command="logout" divided
                    >退出管理系统</el-dropdown-item
                  ></el-dropdown-menu
                ></template
              ></el-dropdown
            >
          </div>
        </div>
        <div class="work-tabs" aria-label="已打开页面">
          <div
            v-for="tab in tabs"
            :key="tab.path"
            class="work-tab"
            :class="{ active: route.path === tab.path }"
          >
            <RouterLink
              :to="tab.to"
              :aria-current="route.path === tab.path ? 'page' : undefined"
              >{{ tab.title }}</RouterLink
            ><button
              v-if="tab.path !== '/'"
              :aria-label="'关闭页签 ' + tab.title"
              @click="closeTab(tab.path)"
            >
              <AdminIcon name="ri:close-line" />
            </button>
          </div>
        </div>
      </div>
      <div id="app-content" class="layout-content"><RouterView /></div>
    </main>
  </div>
</template>
