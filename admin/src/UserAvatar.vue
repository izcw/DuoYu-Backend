<script setup lang="ts">
import { computed, ref, watch } from "vue";
const props = withDefaults(
  defineProps<{ src?: string; name?: string; size?: number }>(),
  { size: 36 },
);
const failed = ref(false);
const image = computed(() => {
  try {
    const u = new URL(props.src || "", location.origin);
    return props.src && u.protocol === "https:"
      ? u.href
      : "/admin/avatar-default.svg";
  } catch {
    return "/admin/avatar-default.svg";
  }
});
watch(
  () => props.src,
  () => (failed.value = false),
);
</script>
<template>
  <img
    class="user-avatar"
    :src="failed ? '/admin/avatar-default.svg' : image"
    :alt="(name || '用户') + '的头像'"
    :style="{ width: size + 'px', height: size + 'px' }"
    referrerpolicy="no-referrer"
    @error="failed = true"
  />
</template>
<style scoped>
.user-avatar {
  display: inline-block;
  border-radius: 50%;
  object-fit: cover;
  flex-shrink: 0;
  background: #f5f5f5;
  vertical-align: middle;
}
</style>
