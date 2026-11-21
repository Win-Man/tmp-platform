<template>
  <div ref="host" class="echart" :style="{ height: height }" />
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import * as echarts from 'echarts'

/** ECharts 轻封装：随容器尺寸自适应，option 变化自动更新。 */
const props = withDefaults(defineProps<{ option: echarts.EChartsOption; height?: string }>(), {
  height: '280px',
})

const host = ref<HTMLDivElement | null>(null)
let chart: echarts.ECharts | null = null
let observer: ResizeObserver | null = null

function render(): void {
  if (!host.value) {
    return
  }
  if (!chart) {
    chart = echarts.init(host.value)
  }
  chart.setOption(props.option, true)
}

onMounted(() => {
  render()
  if (host.value) {
    observer = new ResizeObserver(() => chart?.resize())
    observer.observe(host.value)
  }
})

watch(
  () => props.option,
  () => render(),
  { deep: true },
)

onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
  chart = null
})
</script>

<style scoped>
.echart {
  width: 100%;
}
</style>
