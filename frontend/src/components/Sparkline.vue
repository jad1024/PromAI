<template>
  <div style="position:relative;display:inline-block;vertical-align:middle;">
    <svg :width="width" :height="height" :viewBox="`0 0 ${width} ${height}`" v-if="points.length > 1"
         style="display:block;" @mousemove="onMouseMove" @mouseleave="onMouseLeave">
      <path :d="path" fill="none" :stroke="color" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
      <circle v-if="lastVal !== null" :cx="lastX" :cy="lastY" :r="3" :fill="color" />
      <rect :width="width" :height="height" fill="transparent" style="cursor:crosshair;" />
    </svg>
    <span v-else style="color:var(--text-tertiary);font-size:11px;line-height:36px;">-</span>
    <div v-if="hoverIdx >= 0" class="spark-tooltip" :style="{ left: tooltipLeft + 'px', top: tooltipTop + 'px' }">
      {{ hoverValue }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'

const props = withDefaults(defineProps<{
  data?: [number, number][]
  width?: number
  height?: number
  color?: string
}>(), {
  width: 120,
  height: 36,
  color: '#60a5fa',
})

const points = computed(() => (props.data || []).map(p => [Number(p[0]), Number(p[1])]))
const lastVal = computed(() => points.value.length > 0 ? points.value[points.value.length - 1][1] : null)

const hoverIdx = ref(-1)
const tooltipLeft = ref(0)
const tooltipTop = ref(0)

function scale(arr: number[], range: number): number[] {
  if (arr.length < 2) return arr.map(() => range / 2)
  const min = Math.min(...arr)
  const max = Math.max(...arr)
  const span = max - min || 1
  return arr.map(v => range - ((v - min) / span) * (range - 8) - 4)
}

const scaled = computed(() => {
  if (points.value.length < 2) return { xs: [] as number[], ys: [] as number[] }
  const w = props.width
  const h = props.height
  const values = points.value.map(p => p[1])
  const ys = scale(values, h)
  const xs = values.map((_, i) => (i / (values.length - 1)) * w)
  return { xs, ys }
})

const path = computed(() => {
  const { xs, ys } = scaled.value
  if (xs.length < 2) return ''
  return xs.map((x, i) => `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${ys[i].toFixed(1)}`).join('')
})

const lastX = computed(() => {
  const { xs } = scaled.value
  return xs.length > 0 ? xs[xs.length - 1] : 0
})

const lastY = computed(() => {
  const { ys } = scaled.value
  return ys.length > 0 ? ys[ys.length - 1] : 0
})

function formatVal(v: number) {
  if (typeof v !== 'number' || isNaN(v)) return '-'
  if (Math.abs(v) >= 100) return v.toFixed(1)
  return v.toFixed(3).replace(/0+$/, '').replace(/\.$/, '')
}

const hoverValue = computed(() => {
  if (hoverIdx.value < 0) return ''
  return formatVal(points.value[hoverIdx.value][1])
})

function onMouseMove(e: MouseEvent) {
  const { xs, ys } = scaled.value
  if (xs.length < 2) return
  const svg = e.currentTarget as SVGSVGElement
  const rect = svg.getBoundingClientRect()
  const mx = e.clientX - rect.left
  let idx = 0
  let minDist = Infinity
  for (let i = 0; i < xs.length; i++) {
    const d = Math.abs(xs[i] - mx)
    if (d < minDist) { minDist = d; idx = i }
  }
  hoverIdx.value = idx
  // tooltip 放在点的右上方，若鼠标偏右则放左上方
  const tooltipW = 80
  let lt = xs[idx] + 8
  if (lt + tooltipW > props.width) lt = xs[idx] - tooltipW - 8
  tooltipLeft.value = lt
  tooltipTop.value = Math.max(0, ys[idx] - 30)
}

function onMouseLeave() {
  hoverIdx.value = -1
}
</script>

<style scoped>
.spark-tooltip {
  position: absolute;
  pointer-events: none;
  background: rgba(0,0,0,0.85);
  color: var(--cyan);
  border-radius: 4px;
  padding: 2px 6px;
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
  z-index: 110;
  font-family: 'SF Mono', Monaco, monospace;
  line-height: 1.5;
}
</style>