<script setup lang="ts">
import { computed, ref, watch, onMounted, onUnmounted } from "vue";
import {
  useRoute,
  useRouter,
  onBeforeRouteLeave,
  onBeforeRouteUpdate,
} from "vue-router";
import { ElMessage, ElMessageBox } from "element-plus";
import { api, size, date, session } from "./api";
import UserAvatar from "./UserAvatar.vue";
import { pageName } from "./navigation";
import PageHeading from "./PageHeading.vue";
import AdminSearch from "./AdminSearch.vue";
import FilePreview from "./FilePreview.vue";
const route = useRoute(),
  router = useRouter();
const section = computed(() => String(route.params.section)),
  id = computed(() => String(route.params.id || "")),
  tab = computed(() => String(route.params.tab || "files"));
const title = computed(() => pageName(section.value));
const descriptions:Record<string,string> = {
  users:"查看账号文件、存储配额与访问状态", templates:"从用户数据创建快照，管理发布状态和官方默认模板",
  widgets:"设置组件类型、默认尺寸和可调整范围", wallpapers:"上传预览并管理已发布、草稿和归档壁纸",
  backups:"查看用户同步历史、处理冲突或下载备份", settings:"设置用户文件的默认存储配额", audit:"查看管理员操作的时间、动作及对象"
};
const catalog = computed(() =>
  ["templates", "widgets", "wallpapers"].includes(section.value),
);
const sourceUsers = ref<any[]>([]),
  defaultTemplate = ref("");
const items = ref<any[]>([]),
  jobs = ref<any[]>([]),
  total = ref(0),
  page = ref(1),
  search = ref(""),
  busy = ref(false),
  error = ref(""),
  dirty = ref(false);
const form = ref<any>({}),
  configText = ref("{}"),
  quotaMB = ref(100);
const statusNames: Record<string, string> = {
  published: "已发布",
  draft: "草稿",
  archived: "已归档",
};
const jobStatus: Record<string, string> = {
  queued: "等待中",
  running: "进行中",
  succeeded: "已完成",
  failed: "失败",
};
const backupView = ref("users");
function changeBackupView() {
  if (id.value) {
    void router.push("/backups");
    return;
  }
  if (page.value === 1) void load();
  else page.value = 1;
}
const actionNames:Record<string,string> = {'catalog.upload':'上传壁纸图片','catalog.save':'保存内容','catalog.delete':'删除内容','template.capture':'保存用户模板快照','template.default':'设置默认模板','backup.user.download':'下载用户备份','backup.system.download':'下载系统备份','backup.download':'下载备份','job.create':'创建后台任务','settings.quota':'修改默认配额','user.update':'修改账号设置','asset.read':'预览用户文件','asset.delete':'删除用户文件','admin.login':'管理员登录','admin.logout':'管理员退出'};
const backupKind = (kind: string) =>
  ({
    full: "全量备份",
    incremental: "增量备份",
    "backup-full": "全量备份",
    "backup-incremental": "增量备份",
    backup: "自动备份",
  })[kind] || kind;
const wallpaperStatus = ref("published");
const uploadPreview = ref("");
const wallpaperImage = computed(() => {
  if (uploadPreview.value) return uploadPreview.value;
  try {
    return JSON.parse(configText.value).url || "";
  } catch {
    return "";
  }
});
function releaseUploadPreview() {
  if (uploadPreview.value) URL.revokeObjectURL(uploadPreview.value);
  uploadPreview.value = "";
}
function changeWallpaperStatus() {
  if (page.value === 1) void load();
  else page.value = 1;
}
const inheritQuota = ref(true);
const quotaMode = computed({
  get: () => (inheritQuota.value ? "default" : "custom"),
  set: (value: string) => {
    inheritQuota.value = value === "default";
  },
});
const userProfile = ref<any>({});
const previewFile = ref<any>(null);
const previewUrl = (row: any) =>
  "/api/admin/users/" +
  encodeURIComponent(id.value) +
  "/files/" +
  encodeURIComponent(row.sha256);
const effectiveQuota = computed(() =>
  inheritQuota.value
    ? Number(userProfile.value.default_quota_bytes || 104857600)
    : Number(form.value.quota_mb || 0) * 1048576,
);
const storagePercent = computed(() =>
  effectiveQuota.value > 0
    ? Math.min(
        100,
        Math.round(
          (Number(userProfile.value.storage_bytes || 0) /
            effectiveQuota.value) *
            100,
        ),
      )
    : 0,
);
function quotaModeChanged() {
  if (inheritQuota.value || !Number.isInteger(form.value.quota_mb))
    form.value.quota_mb =
      Number(userProfile.value.default_quota_bytes || 104857600) / 1048576;
  dirty.value = true;
}
function submitSearch() {
  if (page.value === 1) void load();
  else page.value = 1;
}
let loadSeq = 0;
async function load() {
  const seq = ++loadSeq;
  busy.value = true;
  error.value = "";
  dirty.value = false;
  try {
    let path = section.value;
    if (section.value === "backups")
      path = id.value
        ? "backups/users/" + id.value
        : "backups/" + backupView.value;
    if (catalog.value)
      path =
        "catalog/" +
        path +
        (id.value && id.value !== "new" ? "/" + id.value : "");
    else if (section.value === "users" && id.value)
      path = "users/" + id.value + "/" + tab.value;
    if (catalog.value && id.value === "new") {
      form.value = {
        name: "",
        description: "",
        status: "draft",
        version: 0,
        source: String(route.query.source || ""),
      };
      if (section.value === "templates") {
        const result = await api("users");
        if (seq !== loadSeq) return;
        sourceUsers.value = result.items;
      }
      configText.value = JSON.stringify(
        section.value === "wallpapers"
          ? { url: "", credit: "" }
          : section.value === "widgets"
            ? { type: "note", width: 3, height: 2 }
            : {
                format: "duoyu-backup",
                version: 1,
                tables: { libraryPages: [], libraryItems: [] },
              },
        null,
        2,
      );
      if (section.value === "widgets") form.value.widget = {type:"calendar",minWidth:1,minHeight:1,width:3,height:2,maxWidth:5,maxHeight:4};
      items.value = [];
      return;
    }
    const result = await api(
      path +
        "?page=" +
        page.value +
        "&q=" +
        encodeURIComponent(search.value) +
        (section.value === "wallpapers" && !id.value
          ? "&status=" + wallpaperStatus.value
          : ""),
    );
    if (seq !== loadSeq) return;
    items.value = result.items || [];
    total.value = Number(items.value[0]?.total || items.value.length);
    if (catalog.value && id.value) {
      if (!items.value.length) throw Error("资源不存在");
      form.value = { ...items.value[0] };
      form.value.source = form.value.payload?.source_account_id;
      if (section.value === "templates") sourceUsers.value = form.value.source_user ? [form.value.source_user] : [];
      if (section.value === "widgets") form.value.widget = {type:"calendar",minWidth:1,minHeight:1,width:3,height:2,maxWidth:5,maxHeight:4,...form.value.payload};
      configText.value = JSON.stringify(form.value.payload, null, 2);
    }
    if (section.value === "users" && id.value && tab.value === "settings") {
      if (!items.value[0]) throw Error("用户不存在");
      userProfile.value = { ...items.value[0] };
      inheritQuota.value = items.value[0].quota_bytes == null;
      form.value = {
        disabled: items.value[0].disabled,
        quota_mb:
          Number(
            items.value[0].effective_quota_bytes ||
              items.value[0].default_quota_bytes ||
              104857600,
          ) / 1048576,
      };
    }
    if (section.value === "settings")
      quotaMB.value =
        Number(
          items.value.find((r) => r.id === "storage_quota_bytes")?.value ||
            104857600,
        ) / 1048576;
    if (section.value === "templates" && !id.value)
      defaultTemplate.value =
        (await api("settings")).items.find(
          (x: any) => x.id === "default_template_id",
        )?.value || "";
    if (section.value === "backups" && !id.value && backupView.value === "system") {
      const result = await api("jobs");
      if (seq === loadSeq) jobs.value = result.items;
    }
  } catch (e) {
    if (seq === loadSeq)
      error.value = e instanceof Error ? e.message : String(e);
  } finally {
    if (seq === loadSeq) busy.value = false;
  }
}
watch(
  () => route.fullPath,
  () => {
    const resetPage = page.value !== 1;
    page.value = 1;
    search.value = "";
    previewFile.value = null;
    releaseUploadPreview();
    items.value = [];
    total.value = 0;
    sourceUsers.value = [];
    if (!resetPage) void load();
  },
  { immediate: true },
);
watch(page, load);
async function canLeave() {
  if (!dirty.value) return true;
  try {
    await ElMessageBox.confirm("尚有未保存修改，确定离开？", "离开页面", {
      type: "warning",
    });
    return true;
  } catch {
    return false;
  }
}
onBeforeRouteLeave(canLeave);
onBeforeRouteUpdate(canLeave);
async function save() {
  busy.value = true;
  try {
    if (section.value === "templates" && (id.value === "new" || form.value.source !== form.value.payload?.source_account_id)) {
      const result = await api("templates/capture", {
        account_id: form.value.source,
        ...(id.value !== "new" ? {id:id.value,version:Number(form.value.version),status:form.value.status} : {}),
        name: form.value.name,
        description: form.value.description,
      });
      dirty.value = false;
      ElMessage.success("用户数据快照已保存");
      await router.push("/templates/" + result.id);
      return;
    }
    let payload;
    try {
      payload = JSON.parse(configText.value);
    } catch {
      throw Error("配置不是有效 JSON");
    }
    if (!payload || Array.isArray(payload) || typeof payload !== "object")
      throw Error("配置必须是 JSON 对象");
    await api(
      "catalog/" + section.value + (id.value === "new" ? "" : "/" + id.value),
      {
        name: form.value.name,
        description: form.value.description,
        status: form.value.status,
        payload: section.value === "widgets" ? form.value.widget : form.value.payload?.format === "duoyu-template" ? {} : payload,
        version: Number(form.value.version),
      },
    );
    dirty.value = false;
    ElMessage.success("已保存");
    await router.push("/" + section.value);
  } catch (e) {
    ElMessage.error(String(e));
  } finally {
    busy.value = false;
  }
}
let sourceSearch = 0;
async function findUsers(q: string) {
  const request = ++sourceSearch, path = route.fullPath;
  try {
    const result = await api("users?q=" + encodeURIComponent(q));
    if (request !== sourceSearch || route.fullPath !== path) return;
    const selected = sourceUsers.value.find(user => user.id === form.value.source);
    sourceUsers.value = selected && !result.items.some((user:any) => user.id === selected.id)
      ? [selected, ...result.items] : result.items;
  } catch (e) {
    if (request === sourceSearch && route.fullPath === path) ElMessage.error(String(e));
  }
}
async function setDefault(row?: any) {
  try {
    await ElMessageBox.confirm(
      row
        ? "新用户首次初始化时会复制此模板，已有用户不受影响。"
        : "取消新用户默认模板？",
      "官方默认模板",
    );
    await api("templates/default", { id: row?.id || "" });
    ElMessage.success("已更新官方默认模板");
    await load();
  } catch (e) {
    if (e !== "cancel" && e !== "close") ElMessage.error(String(e));
  }
}
async function remove(row: any) {
  try {
    await ElMessageBox.confirm(
      "确认删除「" + row.name + "」？已导入用户桌面的内容不受影响。",
      "删除资源",
      { type: "warning" },
    );
    await api(
      "catalog/" + section.value + "/" + row.id + "?version=" + row.version,
      undefined,
      "DELETE",
    );
    ElMessage.success("已删除");
    await load();
  } catch (e) {
    if (e !== "cancel" && e !== "close") ElMessage.error(String(e));
  }
}
async function userSettings(row: any) {
  await router.push("/users/" + row.id + "/settings");
}
async function deleteUserFile(row: any) {
  const force =
    Number(row.reference_count || 0) > 0 || Number(row.conflict_count || 0) > 0;
  try {
    await ElMessageBox.confirm(
      force
        ? "这张图片仍有引用或待处理冲突。删除会移除云端正在使用它的图标/壁纸图片，并同步到用户各设备；现有备份中的副本不受影响。确认删除？"
        : "删除这张未使用的图片并释放用户配额？",
      "删除用户图片",
      { type: "warning", confirmButtonText: "确认删除" },
    );
    await api(
      previewUrl(row).slice("/api/admin/".length) +
        (force ? "?force=true" : ""),
      undefined,
      "DELETE",
    );
    previewFile.value = null;
    ElMessage.success("已删除，正在清理存储文件");
    await load();
  } catch (e) {
    if (e !== "cancel" && e !== "close") ElMessage.error(String(e));
  }
}
async function saveUser() {
  if (
    error.value ||
    (!inheritQuota.value &&
      (!Number.isInteger(form.value.quota_mb) ||
        form.value.quota_mb < 0 ||
        form.value.quota_mb > 102400))
  ) {
    ElMessage.error("请输入0至102400之间的整数配额");
    return;
  }
  busy.value = true;
  try {
    await api("users/" + id.value, {
      disabled: !!form.value.disabled,
      quota_mb: inheritQuota.value ? null : Number(form.value.quota_mb),
    });
    dirty.value = false;
    ElMessage.success("已更新");
    await router.push("/users");
  } catch (e) {
    ElMessage.error(String(e));
  } finally {
    busy.value = false;
  }
}
async function saveSettings() {
  busy.value = true;
  try {
    await api("settings", { quota_mb: quotaMB.value });
    dirty.value = false;
    ElMessage.success("默认配额已更新");
  } catch (e) {
    ElMessage.error(String(e));
  } finally {
    busy.value = false;
  }
}
async function job(kind: string) {
  try {
    await api("jobs", { kind });
    ElMessage.success("任务已排队");
    await load();
  } catch (e) {
    ElMessage.error(String(e));
  }
}
async function upload(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (!file) return;
  releaseUploadPreview();
  uploadPreview.value = URL.createObjectURL(file);
  busy.value = true;
  try {
    const r = await fetch("/api/admin/catalog-assets", {
      method: "POST",
      headers: { "Content-Type": file.type, "X-CSRF-Token": session.csrf },
      body: file,
    });
    const data = await r.json();
    if (!r.ok) throw Error(data.error);
    const cfg = JSON.parse(configText.value);
    cfg.url = data.url;
    cfg.preview_url = data.preview_url;
    configText.value = JSON.stringify(cfg, null, 2);
    dirty.value = true;
    ElMessage.success("图片已上传");
  } catch (e) {
    releaseUploadPreview();
    ElMessage.error(String(e));
  } finally {
    busy.value = false;
    (event.target as HTMLInputElement).value = "";
  }
}
onUnmounted(releaseUploadPreview);
async function refreshPage() {
  if (await canLeave()) await load();
}
let backupPoll: ReturnType<typeof setInterval> | undefined;
onMounted(() => {
  window.addEventListener("duoyu-admin-refresh", refreshPage);
  backupPoll = setInterval(() => {
    if (
      section.value === "backups" &&
      backupView.value === "system" &&
      !id.value &&
      !busy.value &&
      jobs.value.some((job) => ["queued", "running"].includes(job.status))
    )
      void load();
  }, 3000);
});
onUnmounted(() => clearInterval(backupPoll));
onUnmounted(() =>
  window.removeEventListener("duoyu-admin-refresh", refreshPage),
);
</script>
<template>
  <section class="dy-page art-page-view">
    <RouterLink
      v-if="id"
      class="dy-back-button el-button el-button--default is-text"
      :to="'/' + section"
      >← 返回{{ title }}</RouterLink
    >
    <PageHeading :title="title + (id ? (id === 'new' ? ' / 新建' : ' / 详情') : '')"
      :description="descriptions[section]" :busy="busy" @refresh="refreshPage">
      <el-button
          v-if="catalog && !id"
          type="primary"
          @click="router.push('/' + section + '/new')"
          >{{ section === "templates" ? "从用户创建" : "新增" }}</el-button
        >
    </PageHeading>

        <div v-if="id && (section === 'backups' || (section === 'users' && tab !== 'settings'))" class="tabs">
          <RouterLink :to="'/users/' + id + '/files'">文件</RouterLink
          ><RouterLink :to="'/users/' + id + '/sync'">同步历史</RouterLink
          ><RouterLink :to="'/backups/' + id">数据备份</RouterLink
          ><RouterLink :to="'/users/' + id + '/conflicts'">冲突记录</RouterLink>
        </div>
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      :closable="false"
      show-icon
    />
    <div v-if="catalog && id" class="dy-panel">
      <el-form class="dy-form" label-position="top" @input="dirty = true"
        ><el-form-item label="名称"
          ><el-input v-model="form.name" maxlength="100" /></el-form-item
        ><el-form-item
          v-if="section === 'templates'"
          label="来源用户"
          ><el-select
            v-model="form.source" @change="dirty = true"
            filterable
            remote
            :remote-method="findUsers"
            placeholder="搜索用户或选择来源账号"
            ><el-option
              v-for="u in sourceUsers"
              :key="u.id"
              :label="(u.name || u.subject) + ' · ' + u.id"
              :value="u.id"><UserAvatar :src="u.picture" :name="u.name" :size="24"/> {{ u.name || u.subject || u.id }} · {{ u.email || '未提供邮箱' }}</el-option><template #label><UserAvatar :src="sourceUsers.find(u=>u.id===form.source)?.picture" :size="24"/> {{ sourceUsers.find(u=>u.id===form.source)?.name || form.source }} · {{ sourceUsers.find(u=>u.id===form.source)?.email || '未提供邮箱' }}</template></el-select></el-form-item
        ><el-form-item label="说明"
          ><el-input
            v-model="form.description"
            type="textarea"
            :rows="3" /></el-form-item
        ><el-form-item
          v-if="!(section === 'templates' && id === 'new')"
          label="状态"
          ><el-select v-model="form.status" @change="dirty = true"
            ><el-option label="草稿" value="draft" /><el-option
              label="已发布"
              value="published" /><el-option
              label="已归档"
              value="archived" /></el-select></el-form-item
        ><el-form-item v-if="section === 'wallpapers'" label="壁纸图片">
          <div class="wallpaper-upload">
            <el-image
              v-if="wallpaperImage"
              class="wallpaper-large-preview"
              :src="wallpaperImage"
              fit="contain"
              :preview-src-list="[wallpaperImage]"
              preview-teleported
            >
              <template #error
                ><div class="wallpaper-image-empty">
                  图片无法加载，请重新上传
                </div></template
              >
            </el-image>
            <div v-else class="wallpaper-upload-empty">
              上传壁纸，在这里预览完整图片
            </div>
            <label
              class="el-button wallpaper-upload-button"
              :class="{ 'is-disabled': busy }"
            >
              {{
                busy ? "正在上传…" : wallpaperImage ? "替换壁纸" : "上传壁纸"
              }}
              <input
                type="file"
                aria-label="上传壁纸"
                accept="image/png,image/jpeg,image/webp,image/gif"
                :disabled="busy"
                @change="upload"
              />
            </label>
            <span class="dy-muted"
              >支持 PNG、JPG、WebP、GIF，点击大图可查看原图</span
            >
          </div>
        </el-form-item>
        <template v-if="section === 'widgets' && form.widget">
          <el-form-item label="组件类型"><el-select v-model="form.widget.type" @change="dirty = true"><el-option v-for="(label,key) in {calendar:'日历',weather:'天气',note:'便签'}" :key="key" :label="label" :value="key"/></el-select></el-form-item>
          <el-form-item v-for="([label,w,h]) in [['最小尺寸','minWidth','minHeight'],['默认尺寸','width','height'],['最大尺寸','maxWidth','maxHeight']]" :key="w" :label="label"><el-input-number v-model="form.widget[w]" @change="dirty = true" :min="1" :max="9"/> 列 × <el-input-number v-model="form.widget[h]" @change="dirty = true" :min="1" :max="8"/> 行</el-form-item>
        </template>
        <p v-if="section !== 'wallpapers'" class="dy-muted">
          {{
            section === "widgets"
              ? "组件仅配置扩展支持的类型，不执行远程脚本。"
              : section === "templates"
                ? "来源用户的已同步内容会生成独立快照，草稿审核后再发布。来源用户后续修改不会改变该模板。"
                : "配置 url 和 credit 等展示资料。"
          }}
        </p>
        <div class="dy-form-footer">
          <el-button type="primary" :loading="busy" @click="save"
            >保存</el-button
          ><el-button @click="router.push('/' + section)">取消</el-button>
        </div></el-form
      >
    </div>
    <div v-else-if="section === 'settings'" class="dy-panel">
      <h2>用户默认文件配额</h2>
      <el-form class="dy-form" label-position="top"
        ><el-form-item label="每用户容量（MB）"
          ><el-input-number
            v-model="quotaMB"
            :min="1"
            :max="102400"
            :precision="0"
            @change="dirty = true"
        /></el-form-item>
        <p class="dy-muted">
          默认
          100MB。单独配置的用户沿用个人配额；调低配额不删除已有文件，超限后暂停新增上传。
        </p>
        <el-button type="primary" :loading="busy" @click="saveSettings"
          >保存配置</el-button
        ></el-form
      ><el-divider />
      <h2>唯一超级管理员</h2>
      <p>账号 8888888888 · DuoYu ID</p>
      <p class="dy-muted">
        服务端绑定验证后的内部身份，不提供新增管理员、角色授权或密码入口。
      </p>
    </div>
    <div
      v-else-if="section === 'users' && id && tab === 'settings'"
      class="dy-panel"
    >
      <div class="user-summary">
        <UserAvatar
          :src="userProfile.picture"
          :name="userProfile.name"
          :size="48"
        />
        <div>
          <h2>{{ userProfile.name || "用户访问设置" }}</h2>
          <p class="dy-muted">{{ id }}</p>
        </div>
      </div>
      <el-form class="dy-form quota-form" label-position="top" v-loading="busy">
        <el-form-item label="项目访问"
          ><el-switch
            v-model="form.disabled"
            active-text="暂停访问"
            @change="dirty = true"
        /></el-form-item>
        <el-form-item label="配额方式"
          ><el-radio-group v-model="quotaMode" @change="quotaModeChanged"
            ><el-radio-button value="default">跟随默认</el-radio-button
            ><el-radio-button value="custom"
              >单独设置</el-radio-button
            ></el-radio-group
          ></el-form-item
        >
        <el-form-item label="个人配额（MB）"
          ><el-input-number
            v-model="form.quota_mb"
            :key="quotaMode"
            :disabled="inheritQuota"
            :min="0"
            :max="102400"
            :precision="0"
            @change="dirty = true"
          /><span class="quota-hint">{{
            inheritQuota ? "继承全局默认配额" : "此设置仅对当前用户生效"
          }}</span></el-form-item
        >
        <div class="storage-summary">
          <div>
            <span>已用 {{ size(userProfile.storage_bytes) }}</span
            ><strong>生效容量 {{ size(effectiveQuota) }}</strong>
          </div>
          <el-progress
            :percentage="storagePercent"
            :show-text="false"
            :stroke-width="6"
            :status="
              Number(userProfile.storage_bytes) > effectiveQuota
                ? 'exception'
                : undefined
            "
          />
          <p class="dy-muted">
            {{
              effectiveQuota === 0
                ? "已禁止新增上传，现有文件仍然保留。"
                : Number(userProfile.storage_bytes) > effectiveQuota
                  ? "当前用量已超出配额，新增上传将暂停，已有文件不会删除。"
                  : "降低配额不会删除已有文件；设置为0MB时禁止新增上传。"
            }}
          </p>
        </div>
        <div class="dy-form-footer">
          <el-button
            type="primary"
            :loading="busy"
            :disabled="!!error"
            @click="saveUser"
            >保存设置</el-button
          ><el-button @click="router.push('/users')">取消</el-button>
        </div>
      </el-form>
    </div>
    <div v-else-if="section === 'backups'" class="dy-panel">
      <el-tabs v-if="!id" v-model="backupView" @tab-change="changeBackupView">
        <el-tab-pane label="用户数据备份" name="users" />
        <el-tab-pane label="系统备份" name="system" />
      </el-tabs>
      <template v-if="backupView === 'users' || id">
        <p class="dy-muted">
          每次同步实际更改的数据会生成一份备份，每位用户保留最近 5 份。下载包含桌面数据和引用的图片。
        </p>
        <div v-if="!id" class="dy-toolbar">
          <AdminSearch v-model="search" placeholder="搜索昵称或账号" @search="submitSearch" />
        </div>
        <el-table
          v-if="!id"
          :data="items"
          v-loading="busy"
          empty-text="暂无用户"
        >
          <el-table-column label="用户" min-width="220"
            ><template #default="{ row }"
              ><div class="backup-user">
                <UserAvatar
                  :name="row.name || row.subject"
                  :src="row.picture"
                /><span>{{ row.name || row.subject }}</span>
              </div></template
            ></el-table-column
          >
          <el-table-column label="备份数量" width="140"
            ><template #default="{ row }"
              >{{ row.backup_count }} / 5</template
            ></el-table-column
          >
          <el-table-column label="最近备份" min-width="180"
            ><template #default="{ row }">{{
              row.latest_backup ? date(row.latest_backup) : "尚无数据更新"
            }}</template></el-table-column
          >
          <el-table-column label="操作" min-width="210"
            ><template #default="{ row }"><RouterLink :to="'/users/' + row.id + '/sync'">同步历史</RouterLink> · <RouterLink :to="'/users/' + row.id + '/conflicts'">冲突记录</RouterLink> · <el-button link @click="router.push('/backups/' + row.id)"
                >查看备份</el-button
              ></template
            ></el-table-column
          >
        </el-table>
        <el-table
          v-else
          :data="items"
          v-loading="busy"
          empty-text="该用户尚无备份，下一次成功上传数据后自动创建"
        >
          <el-table-column label="备份时间" min-width="180"
            ><template #default="{ row }">{{
              date(row.created_at)
            }}</template></el-table-column
          >
          <el-table-column prop="revision" label="数据版本" width="120" />
          <el-table-column prop="entity_count" label="数据项" width="120" />
          <el-table-column label="操作" width="120"
            ><template #default="{ row }"
              ><a :href="'/api/admin/backups/users/' + id + '/' + row.id"
                >下载备份</a
              ></template
            ></el-table-column
          >
        </el-table>
        <el-pagination
          v-if="!id && total > 20"
          v-model:current-page="page"
          :page-size="20"
          :total="total"
          layout="prev,pager,next,total"
        />
      </template>
      <template v-else>
        <p class="dy-muted">
          每天北京时间 00:00 自动备份；首次及每隔 7
          天全量备份，其余为增量备份，最多保留 7
          条恢复记录。每份下载包含独立恢复所需的全部加密文件块。
        </p>
        <div class="dy-toolbar">
          <el-button type="primary" :disabled="busy" @click="job('backup-full')"
            >创建全量备份</el-button
          >
          <el-button :disabled="busy" @click="job('backup-incremental')"
            >创建增量备份</el-button
          >
        </div>
        <el-table :data="items" v-loading="busy" empty-text="尚无系统备份">
          <el-table-column label="类型" width="140"
            ><template #default="{ row }"
              ><span class="backup-kind">{{
                backupKind(row.kind)
              }}</span></template
            ></el-table-column
          >
          <el-table-column label="备份时间" min-width="180"
            ><template #default="{ row }">{{
              date(row.created_at)
            }}</template></el-table-column
          >
          <el-table-column label="完整数据量" min-width="130"
            ><template #default="{ row }">{{
              size(row.bytes)
            }}</template></el-table-column
          >
          <el-table-column label="本次新增存储" min-width="140"
            ><template #default="{ row }">{{
              size(row.added_bytes)
            }}</template></el-table-column
          >
          <el-table-column label="操作" width="120"
            ><template #default="{ row }"
              ><a :href="'/api/admin/backups/system/' + row.id"
                >下载备份</a
              ></template
            ></el-table-column
          >
        </el-table>
        <h3>手动备份任务</h3>
        <el-table :data="jobs" empty-text="暂无任务">
          <el-table-column label="类型"
            ><template #default="{ row }">{{
              backupKind(row.kind)
            }}</template></el-table-column
          >
          <el-table-column label="状态"
            ><template #default="{ row }">{{
              jobStatus[row.status] || row.status
            }}</template></el-table-column
          >
          <el-table-column label="创建时间" min-width="180"
            ><template #default="{ row }">{{
              date(row.created_at)
            }}</template></el-table-column
          >
          <el-table-column prop="error" label="说明" />
        </el-table>
      </template>
    </div>
    <template v-else>
      <div class="dy-panel">
        <p v-if="section === 'templates'">
          <strong>官方默认模板：</strong
          >{{
            defaultTemplate
              ? items.find((r) => r.id === defaultTemplate)?.name ||
                defaultTemplate
              : "未设置"
          }}
          <el-button v-if="defaultTemplate" link @click="setDefault()"
            >取消默认</el-button
          >
        </p>
        <div
          v-if="catalog || (section === 'users' && !id)"
          class="dy-toolbar"
        >
          <AdminSearch v-model="search" placeholder="搜索" @search="submitSearch" />
        </div>

        <template v-if="section === 'wallpapers'">
          <el-tabs
            v-model="wallpaperStatus"
            @tab-change="changeWallpaperStatus"
          >
            <el-tab-pane label="已发布" name="published" />
            <el-tab-pane label="未发布" name="draft" />
            <el-tab-pane label="归档" name="archived" />
          </el-tabs>
          <div v-loading="busy" class="wallpaper-cards">
            <article v-for="row in items" :key="row.id" class="wallpaper-card">
              <el-image
                :src="
                  row.payload?.preview_url ||
                  (row.payload?.url ? row.payload.url + '?preview=1' : '')
                "
                fit="cover"
                :preview-src-list="row.payload?.url ? [row.payload.url] : []"
                preview-teleported
                lazy
              >
                <template #error
                  ><div class="wallpaper-image-empty">暂无预览</div></template
                >
              </el-image>
              <div class="wallpaper-card-info">
                <h3>{{ row.name }}</h3>
                <p>{{ row.description || "暂无说明" }}</p>
                <small>{{ date(row.updated_at) }}</small>
                <div class="wallpaper-card-actions">
                  <el-button @click.stop="router.push('/wallpapers/' + row.id)"
                    >编辑</el-button
                  >
                  <el-button type="danger" text @click.stop="remove(row)"
                    >删除</el-button
                  >
                </div>
              </div>
            </article>
          </div>
          <el-empty v-if="!busy && !items.length" description="暂无壁纸" />
        </template>
        <el-table v-else :data="items" v-loading="busy" empty-text="暂无数据"
          ><el-table-column
            label="#"
            type="index"
            :index="(n: number) => (page - 1) * 20 + n + 1"
            width="60"
          />
          <template v-if="catalog">
            <el-table-column v-if="section==='templates'" label="来源用户" min-width="220"><template #default="{row}"><UserAvatar :src="row.source_user?.picture" :name="row.source_user?.name" :size="28"/> {{ row.source_user?.name || row.payload?.source_account_id || '未指定' }} <small>{{ row.source_user?.email || '未提供邮箱' }}</small></template></el-table-column><el-table-column
              prop="name"
              label="名称"
              min-width="160"
            /><el-table-column
              prop="description"
              label="说明"
              min-width="200"
              show-overflow-tooltip
            /><el-table-column label="状态" width="120"
              ><template #default="{ row }"
                ><el-tag
                  :type="
                    row.status === 'published'
                      ? 'success'
                      : row.status === 'draft'
                        ? 'info'
                        : 'warning'
                  "
                  effect="light"
                  >{{ statusNames[row.status] || row.status }}</el-tag
                ></template
              ></el-table-column
            ><el-table-column label="更新于" min-width="180"
              ><template #default="{ row }">{{
                date(row.updated_at)
              }}</template></el-table-column
            ><el-table-column
              label="操作"
              :width="section === 'templates' ? 260 : 160"
              ><template #default="{ row }"
                ><el-button
                  text
                  @click="router.push('/' + section + '/' + row.id)"
                  >编辑</el-button
                ><el-button
                  v-if="
                    section === 'templates' &&
                    row.status === 'published' &&
                    row.payload?.format === 'duoyu-template'
                  "
                  text
                  @click="setDefault(row)"
                  >{{
                    defaultTemplate === row.id ? "官方默认" : "设为默认"
                  }}</el-button
                ><el-button text type="danger" @click="remove(row)"
                  >删除</el-button
                ></template
              ></el-table-column
            ></template
          >
          <template
            v-else-if="section === 'users' && !id"
            ><el-table-column label="用户" min-width="180"
              ><template #default="{ row }"
                ><div class="user-identity">
                  <UserAvatar
                    :src="row.picture"
                    :name="row.name"
                    :size="36"
                  /><span>{{ row.name || "未设置昵称" }}</span>
                </div></template
              ></el-table-column
            ><el-table-column
              prop="subject"
              label="DuoYu ID 内部身份"
              min-width="200"
              show-overflow-tooltip
            /><el-table-column label="文件使用量" min-width="130"
              ><template #default="{ row }">{{
                size(row.storage_bytes)
              }}</template></el-table-column
            ><el-table-column
              prop="head_revision"
              label="修订号"
              width="100"
            /><el-table-column label="状态" width="100"
              ><template #default="{ row }"
                ><el-tag
                  :type="row.disabled ? 'warning' : 'success'"
                  effect="light"
                  >{{ row.disabled ? "已暂停" : "正常" }}</el-tag
                ></template
              ></el-table-column
            ><el-table-column label="操作" width="310"
              ><template #default="{ row }"
                ><el-button
                  text
                  @click="router.push('/users/' + row.id + '/files')"
                  >文件</el-button
                ><el-button
                  text
                  @click="router.push('/users/' + row.id + '/sync')"
                  >同步</el-button
                ><el-button text @click="userSettings(row)">设置</el-button
                ><el-button
                  text
                  @click="router.push('/templates/new?source=' + row.id)"
                  >设为模板</el-button
                ></template
              ></el-table-column
            ></template
          >
          <template v-else-if="section === 'audit'"
            ><el-table-column
               :formatter="(row:any) => actionNames[row.action] || row.action"
              label="操作"
              min-width="160"
            /><el-table-column
              prop="target"
              label="对象"
              min-width="220"
              show-overflow-tooltip
            /><el-table-column label="时间" min-width="180"
              ><template #default="{ row }">{{
                date(row.created_at)
              }}</template></el-table-column
            ></template
          >
          <template v-else-if="tab === 'files'">
            <el-table-column label="预览" width="100"
              ><template #default="{ row }"
                ><button
                  class="file-thumb-button"
                  aria-label="预览图片"
                  @click="previewFile = row"
                >
                  <el-image :src="previewUrl(row)" fit="cover" lazy
                    ><template #error
                      ><span class="file-thumb-error">加载失败</span></template
                    ></el-image
                  >
                </button></template
              ></el-table-column
            >
            <el-table-column
              prop="sha256"
              label="文件标识"
              min-width="200"
              show-overflow-tooltip
            />
            <el-table-column prop="media_type" label="格式" width="130" />
            <el-table-column label="大小" width="110"
              ><template #default="{ row }">{{
                size(row.byte_size)
              }}</template></el-table-column
            >
            <el-table-column label="上传时间" min-width="180"
              ><template #default="{ row }">{{
                date(row.created_at)
              }}</template></el-table-column
            >
            <el-table-column label="使用状态" width="110"
              ><template #default="{ row }"
                ><el-tag :type="row.reference_count ? 'success' : 'info'">{{
                  row.reference_count
                    ? "使用中 · " + row.reference_count
                    : "未使用"
                }}</el-tag></template
              ></el-table-column
            >
            <el-table-column label="操作" width="170"
              ><template #default="{ row }"
                ><el-button text @click="previewFile = row">预览</el-button
                ><el-button text type="danger" @click="deleteUserFile(row)"
                  >删除</el-button
                ></template
              ></el-table-column
            >
          </template>
          <template v-else-if="tab === 'sync'"
            ><el-table-column prop="revision" label="修订号" /><el-table-column
              prop="operation_id"
              label="操作ID"
              min-width="280"
            /><el-table-column
              prop="changes"
              label="变更数量"
            /><el-table-column label="时间" min-width="180"
              ><template #default="{ row }">{{
                date(row.created_at)
              }}</template></el-table-column
            ></template
          >
          <template v-else
            ><el-table-column
              prop="operation_id"
              label="操作ID"
              min-width="280"
            /><el-table-column label="原因"
              ><template #default="{ row }">{{
                row.detail?.code
              }}</template></el-table-column
            ><el-table-column label="状态"
              ><template #default="{ row }">{{
                row.resolved_at ? "已处理" : "待用户处理"
              }}</template></el-table-column
            ><el-table-column label="时间" min-width="180"
              ><template #default="{ row }">{{
                date(row.created_at)
              }}</template></el-table-column
            ></template
          > </el-table
        ><el-pagination
          v-if="total > 20"
          v-model:current-page="page"
          :page-size="20"
          :total="total"
          layout="prev,pager,next,total"
        /></div
    ></template>
    <FilePreview
      :file="previewFile"
      :url="previewFile ? previewUrl(previewFile) : ''"
      @close="previewFile = null"
    />
  </section>
</template>

<style scoped>
.backup-kind {
  color: #303133;
  font-weight: 500;
}
.backup-user {
  display: flex;
  align-items: center;
  gap: 12px;
}
.wallpaper-cards {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 20px;
  min-height: 80px;
}
.wallpaper-card {
  position: relative;
  isolation: isolate;
  border: 0;
  border-radius: 20px;
  overflow: hidden;
  background: #e9ecef;
  aspect-ratio: 16/9;
  box-shadow: 0 7px 20px rgb(0 0 0 / 9%);
}
.wallpaper-card > .el-image {
  display: block;
  width: 100%;
  height: 100%;
  cursor: zoom-in;
}
.wallpaper-card::after {
  content: "";
  position: absolute;
  inset: 0;
  pointer-events: none;
  background: linear-gradient(
    180deg,
    transparent 35%,
    rgb(0 0 0 / 16%) 58%,
    rgb(0 0 0 / 70%) 100%
  );
}
.wallpaper-card-info {
  position: absolute;
  inset: 14px 16px;
  display: flex;
  flex-direction: column;
  justify-content: flex-end;
  z-index: 1;
  pointer-events: none;
  color: #fff;
}
.wallpaper-card-info h3 {
  margin: 0 0 4px;
  font-size: 17px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: inherit;
}
.wallpaper-card-info p {
  margin: 0 0 6px;
  font-size: 13px;
  line-height: 1.4;
  color: rgb(255 255 255 / 90%);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.wallpaper-card-info small {
  font-size: 11px;
  color: rgb(255 255 255 / 72%);
}
.wallpaper-card-actions {
  position: absolute;
  right: 0;
  top: 0;
  display: flex;
  gap: 8px;
  opacity: 0;
  visibility: hidden;
  transform: translateY(4px);
  transition:
    opacity 0.16s,
    transform 0.16s,
    visibility 0.16s;
  pointer-events: auto;
}
.wallpaper-card:hover .wallpaper-card-actions,
.wallpaper-card:focus-within .wallpaper-card-actions {
  opacity: 1;
  visibility: visible;
  transform: translateY(0);
}
.wallpaper-card-actions :deep(.el-button) {
  margin: 0;
  border: 0;
  border-radius: 8px;
  background: rgb(255 255 255 / 90%);
  color: #303133;
  backdrop-filter: blur(12px);
}
.wallpaper-card-actions :deep(.el-button--danger) {
  color: #e34d59;
}
@media (hover: none) {
  .wallpaper-card .wallpaper-card-actions {
    opacity: 1;
    visibility: visible;
    transform: none;
  }
}
.wallpaper-upload {
  width: 100%;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 16px;
}
.wallpaper-large-preview {
  width: 100%;
  height: clamp(240px, 42vh, 520px);
  background: #f5f5f7;
  border-radius: 12px;
  cursor: zoom-in;
}
.wallpaper-upload-empty {
  width: 100%;
  height: 280px;
  display: grid;
  place-items: center;
  background: #f5f5f7;
  border-radius: 12px;
  color: #9096a0;
}
.wallpaper-image-empty {
  display: grid;
  place-items: center;
  width: 100%;
  height: 100%;
  color: #9096a0;
}
.wallpaper-upload-button input {
  display: none;
}
@media (max-width: 1100px) {
  .wallpaper-cards {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 600px) {
  .wallpaper-cards {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
