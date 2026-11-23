<script setup>
import IconMark from './IconMark.vue'
defineProps({ themes: { type: Array, required: true }, activeIndex: { type: Number, required: true } })
defineEmits(['select'])
</script>
<template>
  <div class="preview-caption">
    <span class="preview-label"><i aria-hidden="true"></i>互动预览</span>
    <div class="theme-switcher" role="group" aria-label="预览壁纸">
      <span class="wall-name" aria-live="polite"><IconMark name="image"/>{{ themes[activeIndex].name }}</span>
      <div class="swatches"><button v-for="(item, index) in themes" :key="item.file" class="swatch" type="button" :class="{ selected: activeIndex === index }" :style="{ '--swatch': item.color }" :aria-label="'切换到' + item.name + '壁纸'" :aria-pressed="index === activeIndex" :title="item.name" @click="$emit('select',index)"></button></div>
    </div>
  </div>
</template>
