<script setup>
defineProps({ name: { type: String, required: true } })
</script>
<template><svg class="icon" aria-hidden="true" focusable="false"><use :href="'#i-' + name" /></svg></template>
