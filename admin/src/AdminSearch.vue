<script setup lang="ts">
const query = defineModel<string>({ required: true });
defineProps<{ placeholder: string }>();
const emit = defineEmits<{ search: [] }>();
</script>
<template>
  <form class="admin-search" role="search" @submit.prevent="emit('search')">
    <el-input v-model="query" :placeholder="placeholder" :aria-label="placeholder" clearable @clear="emit('search')" />
    <el-button native-type="submit">查询</el-button>
  </form>
</template>
