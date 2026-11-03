<script setup lang="ts">
defineProps<{ title: string; description?: string; busy: boolean }>();
defineEmits<{ refresh: [] }>();
</script>
<template>
  <header class="dy-heading">
    <div><h1>{{ title }}</h1><p v-if="description">{{ description }}</p></div>
    <div class="dy-toolbar"><el-button :loading="busy" @click="$emit('refresh')">刷新</el-button><slot /></div>
  </header>
</template>
