<script setup lang="ts">
import { Icon, addCollection } from '@iconify/vue/offline';
import icons from './assets/local-icons.json';
addCollection(icons);
defineProps<{ name: string }>();
</script>
<template>
  <Icon :icon="name" class="art-svg-icon" aria-hidden="true" />
</template>
<style scoped>
.art-svg-icon {
  display: inline-block;
  flex-shrink: 0;
  font-size: 20px;
  vertical-align: middle;
}
</style>
