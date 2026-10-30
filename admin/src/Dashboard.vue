<script setup lang="ts">
import { onMounted, onUnmounted, ref, nextTick } from 'vue';
import * as echarts from 'echarts/core';
import { LineChart } from 'echarts/charts';
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
} from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';
import { api, size } from './api';
echarts.use([
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  CanvasRenderer,
]);
const stats = ref<Record<string, any>>({}),
  error = ref(''),
  busy = ref(false),
  chartEl = ref<HTMLElement>();
let chart: echarts.ECharts | undefined;
let observer: ResizeObserver | undefined;
const cards = [
  ['users', '同步用户'],
  ['entities', '有效数据项'],
  ['storage_bytes', '用户文件'],
  ['conflicts', '待处理冲突'],
  ['operations', '累计操作'],
  ['catalog', '已发布资源'],
];
async function load() {
  busy.value = true;
  error.value = '';
  try {
    const [overview, series] = await Promise.all([
      api('overview'),
      api('charts'),
    ]);
    stats.value = overview;
    await nextTick();
    if (chartEl.value) {
      chart ??= echarts.init(chartEl.value);
      chart.setOption({
        color: ['#111111', '#9ca3af'],
        tooltip: { trigger: 'axis' },
        legend: { data: ['同步操作', '活跃用户'], bottom: 0 },
        grid: { left: 42, right: 24, top: 24, bottom: 55 },
        xAxis: {
          type: 'category',
          data: series.items.map((r: any) => r.day.slice(5)),
          axisLine: { lineStyle: { color: '#e5e7eb' } },
        },
        yAxis: {
          type: 'value',
          minInterval: 1,
          splitLine: { lineStyle: { color: '#f0f0f0' } },
        },
        series: [
          {
            name: '同步操作',
            type: 'line',
            smooth: true,
            symbol: 'none',
            data: series.items.map((r: any) => r.operations),
          },
          {
            name: '活跃用户',
            type: 'line',
            smooth: true,
            symbol: 'none',
            data: series.items.map((r: any) => r.users),
          },
        ],
      });
      observer ??= new ResizeObserver(() => chart?.resize());
      observer.observe(chartEl.value);
    }
  } catch (e) {
    error.value = String(e);
  } finally {
    busy.value = false;
  }
}
onMounted(() => {
  load();
  window.addEventListener('duoyu-admin-refresh', load);
});
onUnmounted(() => {
  window.removeEventListener('duoyu-admin-refresh', load);
  observer?.disconnect();
  chart?.dispose();
});
</script>
<template>
  <section class="dy-page art-page-view">
    <div class="dy-heading">
      <div>
        <h1>概览</h1>
        <p>存储、同步与内容发布</p>
      </div>
      <el-button :loading="busy" @click="load">刷新</el-button>
    </div>
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      show-icon
      :closable="false"
    />
    <div class="dy-stats metrics-grid">
      <div v-for="[key, label] in cards" :key="key" class="dy-stat">
        <span>{{ label }}</span
        ><strong>{{
          key === 'storage_bytes' ? size(stats[key]) : (stats[key] ?? '—')
        }}</strong>
      </div>
    </div>
    <section class="dy-panel">
      <h2>快捷入口</h2>
      <div class="dy-shortcuts">
        <RouterLink class="dy-shortcut" to="/users"
          ><strong>用户管理</strong
          ><span>查看同步用户与存储配额</span></RouterLink
        >
        <RouterLink class="dy-shortcut" to="/wallpapers"
          ><b>壁纸管理</b><span>审核与发布官方壁纸</span></RouterLink
        >
        <RouterLink class="dy-shortcut" to="/backups"
          ><strong>备份管理</strong
          ><span>查看备份记录与恢复任务</span></RouterLink
        >
      </div>
    </section>
    <section class="dy-panel">
      <h2>最近 30 天同步趋势</h2>
      <p class="dy-muted">按数据库日期统计已提交的同步操作及参与用户</p>
      <div ref="chartEl" class="chart" />
    </section>
  </section>
</template>
