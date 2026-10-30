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
const route = useRoute(),
  router = useRouter();
const section = computed(() => String(route.params.section)),
  id = computed(() => String(route.params.id || "")),
  tab = computed(() => String(route.params.tab || "files"));
const labels: Record<string, string> = {
  users: "用户管理",
  templates: "模板管理",
  widgets: "组件管理",
  wallpapers: "壁纸管理",
  backups: "备份管理",
  sync: "同步管理",
  settings: "存储设置",
  audit: "操作记录",
};
const title = computed(() => labels[section.value] || "管理");
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
let loadSeq = 0;
async function load() {
  const seq = ++loadSeq;
  busy.value = true;
  error.value = "";
  dirty.value = false;
  try {
    let path = section.value;
    if (catalog.value)
      path =
        "catalog/" +
        path +
        (id.value && id.value !== "new" ? "/" + id.value : "");
    else if (section.value === "sync") path = "users";
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
      if (section.value === "templates")
        sourceUsers.value = (await api("users")).items;
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
      items.value = [];
      return;
    }
    const result = await api(
      path + "?page=" + page.value + "&q=" + encodeURIComponent(search.value),
    );
    if (seq !== loadSeq) return;
    items.value = result.items || [];
    total.value = Number(items.value[0]?.total || items.value.length);
    if (catalog.value && id.value) {
      if (!items.value.length) throw Error("资源不存在");
      form.value = { ...items.value[0] };
      configText.value = JSON.stringify(form.value.payload, null, 2);
    }
    if (
      section.value === "users" &&
      id.value &&
      tab.value === "settings" &&
      items.value[0]
    )
      form.value = {
        disabled: items.value[0].disabled,
        quota_mb:
          items.value[0].quota_bytes == null
            ? null
            : Number(items.value[0].quota_bytes) / 1048576,
      };
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
    if (section.value === "backups") jobs.value = (await api("jobs")).items;
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
    page.value = 1;
    search.value = "";
    void load();
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
    if (section.value === "templates" && id.value === "new") {
      const result = await api("templates/capture", {
        account_id: form.value.source,
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
        payload: form.value.payload?.format === "duoyu-template" ? {} : payload,
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
async function findUsers(q: string) {
  sourceUsers.value = (await api("users?q=" + encodeURIComponent(q))).items;
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
  form.value = {
    disabled: row.disabled,
    quota_mb:
      row.quota_bytes == null ? null : Number(row.quota_bytes) / 1048576,
  };
}
async function saveUser() {
  busy.value = true;
  try {
    await api("users/" + id.value, {
      disabled: !!form.value.disabled,
      quota_mb:
        form.value.quota_mb === null ? null : Number(form.value.quota_mb),
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
    configText.value = JSON.stringify(cfg, null, 2);
    dirty.value = true;
    ElMessage.success("图片已上传");
  } catch (e) {
    ElMessage.error(String(e));
  } finally {
    busy.value = false;
  }
}
async function refreshPage() {
  if (await canLeave()) await load();
}
onMounted(() => window.addEventListener("duoyu-admin-refresh", refreshPage));
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
    <div class="dy-heading">
      <div>
        <h1>
          {{ title }}{{ id ? (id === "new" ? " / 新建" : " / 详情") : "" }}
        </h1>
        <p v-if="section === 'users'">
          项目账号与文件配额；账号身份由 DuoYu ID 管理
        </p>
        <p v-else-if="section === 'sync'">
          查看修订和冲突，停用账号可暂停其全部同步访问
        </p>
      </div>
      <div class="dy-toolbar">
        <el-button :loading="busy" @click="refreshPage">刷新</el-button
        ><el-button
          v-if="catalog && !id"
          type="primary"
          @click="router.push('/' + section + '/new')"
          >{{ section === "templates" ? "从用户创建" : "新增" }}</el-button
        >
      </div>
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
          v-if="section === 'templates' && id === 'new'"
          label="来源用户"
          ><el-select
            v-model="form.source"
            filterable
            remote
            :remote-method="findUsers"
            placeholder="搜索用户或选择来源账号"
            ><el-option
              v-for="u in sourceUsers"
              :key="u.id"
              :label="(u.name || u.subject) + ' · ' + u.id"
              :value="u.id" /></el-select></el-form-item
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
        ><el-form-item v-if="section === 'wallpapers'" label="壁纸图片"
          ><input
            type="file"
            accept="image/png,image/jpeg,image/webp,image/gif"
            @change="upload"
          />
          <p class="dy-muted">
            上传后自动填写配置的 url。发布后可公开下载，请勿上传用户私密文件。
          </p></el-form-item
        ><el-form-item
          v-if="!(section === 'templates' && id === 'new')"
          label="资源配置 JSON"
          ><el-input
            v-model="configText"
            type="textarea"
            :rows="14"
            :readonly="form.payload?.format === 'duoyu-template'"
            class="json-input"
            @input="dirty = true"
        /></el-form-item>
        <p class="dy-muted">
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
      <h2>项目访问及配额</h2>
      <p class="dy-muted">{{ id }}</p>
      <el-form class="dy-form" label-position="top"
        ><el-form-item label="暂停本项目访问"
          ><el-switch
            v-model="form.disabled"
            @change="dirty = true" /></el-form-item
        ><el-form-item label="个人配额（MB）"
          ><el-input-number
            v-model="form.quota_mb"
            :min="0"
            :max="102400"
            :precision="0"
            @change="dirty = true"
          /><el-button
            @click="
              form.quota_mb = null;
              dirty = true;
            "
            >使用默认配额</el-button
          ></el-form-item
        >
        <p>
          {{
            form.quota_mb == null ? "当前使用默认配额" : "当前使用个人配额"
          }}；0MB 禁止新增文件，不删除原有文件。
        </p>
        <el-button type="primary" :loading="busy" @click="saveUser"
          >保存</el-button
        ></el-form
      >
    </div>
    <template v-else
      ><div v-if="section === 'backups'" class="dy-panel">
        <h2>加密备份</h2>
        <p class="dy-muted">
          数据库和用户图片分别加密备份，需同时保管。恢复会影响全部用户，使用服务器恢复工具并先验证备份。
        </p>
        <div class="dy-toolbar">
          <el-button type="primary" @click="job('backup')"
            >立即创建备份</el-button
          ><el-button @click="job('asset-migration')"
            >迁移旧图片到文件存储</el-button
          >
        </div>
        <el-table :data="jobs" empty-text="暂无任务"
          ><el-table-column prop="kind" label="任务" /><el-table-column
            prop="status"
            label="状态" /><el-table-column label="创建时间"
            ><template #default="{ row }">{{
              date(row.created_at)
            }}</template></el-table-column
          ><el-table-column prop="error" label="结果说明"
        /></el-table>
      </div>
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
          v-if="catalog || (['users', 'sync'].includes(section) && !id)"
          class="dy-toolbar"
        >
          <el-input
            v-model="search"
            placeholder="搜索"
            clearable
            @keyup.enter="
              page = 1;
              load();
            "
          /><el-button
            @click="
              page = 1;
              load();
            "
            >查询</el-button
          >
        </div>
        <div v-if="section === 'users' && id" class="tabs">
          <RouterLink :to="'/users/' + id + '/files'">文件</RouterLink
          ><RouterLink :to="'/users/' + id + '/sync'">同步历史</RouterLink
          ><RouterLink :to="'/users/' + id + '/conflicts'">冲突记录</RouterLink>
        </div>
        <el-table :data="items" v-loading="busy" empty-text="暂无数据"
          ><el-table-column
            label="#"
            type="index"
            :index="(n: number) => (page - 1) * 20 + n + 1"
            width="60"
          />
          <template v-if="catalog"
            ><el-table-column
              prop="name"
              label="名称"
              min-width="160"
            /><el-table-column
              prop="description"
              label="说明"
              min-width="200"
              show-overflow-tooltip
            /><el-table-column
              prop="status"
              label="状态"
              width="120"
            /><el-table-column label="更新于" min-width="180"
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
            v-else-if="(section === 'users' && !id) || section === 'sync'"
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
              ><template #default="{ row }">{{
                row.disabled ? "已暂停" : "正常"
              }}</template></el-table-column
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
          <template v-else-if="section === 'backups'"
            ><el-table-column
              prop="name"
              label="备份文件"
              min-width="300"
            /><el-table-column label="大小" width="130"
              ><template #default="{ row }">{{
                size(row.bytes)
              }}</template></el-table-column
            ><el-table-column label="备份时间" min-width="180"
              ><template #default="{ row }">{{
                date(row.modified)
              }}</template></el-table-column
            ><el-table-column label="操作" width="100"
              ><template #default="{ row }"
                ><a :href="'/api/admin/backups/' + row.name">下载</a></template
              ></el-table-column
            ></template
          >
          <template v-else-if="section === 'audit'"
            ><el-table-column
              prop="action"
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
          <template v-else-if="tab === 'files'"
            ><el-table-column
              prop="sha256"
              label="文件哈希"
              min-width="300"
              show-overflow-tooltip
            /><el-table-column
              prop="media_type"
              label="格式"
              width="140"
            /><el-table-column label="大小" width="130"
              ><template #default="{ row }">{{
                size(row.byte_size)
              }}</template></el-table-column
            ><el-table-column label="查看" width="100"
              ><template #default="{ row }"
                ><a
                  :href="'/api/admin/users/' + id + '/files/' + row.sha256"
                  target="_blank"
                  rel="noopener"
                  >预览</a
                ></template
              ></el-table-column
            ></template
          >
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
  </section>
</template>
