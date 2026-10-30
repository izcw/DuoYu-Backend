<script setup lang="ts">
import { ref, watch } from "vue";
import { size, date } from "./api";
const props = defineProps<{ file: any; url: string }>();
defineEmits<{ close: [] }>();
const dimensions = ref("");
watch(
  () => props.url,
  () => (dimensions.value = ""),
);
function loaded(e: Event) {
  const img = e.target as HTMLImageElement;
  dimensions.value = img.naturalWidth + " × " + img.naturalHeight + " px";
}
</script>
<template>
  <el-dialog
    :model-value="!!file"
    title="文件预览"
    width="min(920px, calc(100vw - 32px))"
    class="file-preview-dialog"
    destroy-on-close
    align-center
    @close="$emit('close')"
    ><template v-if="file"
      ><div class="file-preview-stage">
        <el-image :src="url" fit="contain" @load="loaded"
          ><template #placeholder
            ><span class="preview-state">图片加载中…</span></template
          ><template #error
            ><div class="preview-state">
              <strong>图片暂时无法加载</strong
              ><span>文件可能已被移除，或登录已过期，请关闭后刷新重试。</span>
            </div></template
          ></el-image
        >
      </div>
      <div class="file-preview-meta">
        <span
          >{{ file.media_type }} · {{ size(file.byte_size)
          }}<template v-if="dimensions"> · {{ dimensions }}</template></span
        ><span>{{ date(file.created_at) }}</span>
      </div>
      <p class="file-preview-hash" :title="file.sha256">
        {{ file.sha256 }}
      </p></template
    ><template #footer
      ><el-button @click="$emit('close')">关闭</el-button></template
    ></el-dialog
  >
</template>
