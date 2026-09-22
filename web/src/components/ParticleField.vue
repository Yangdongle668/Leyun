<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'

/**
 * 微粒连线背景。
 *
 * 一堆缓慢漂移的点，靠得够近就连一条线，随距离淡出。
 * 只用 canvas，不引第三方库——这点东西值不上一个依赖。
 *
 * 几条自我约束，免得一个装饰背景拖垮整页：
 *   · 页面切到后台就停：看不见的动画不该继续烧 CPU 和电
 *   · 尊重"减少动态效果"的系统设置，此时只画一帧静态的
 *   · 粒子数按面积算并封顶，窄屏不会和宽屏一样密
 *   · 连线判定是 O(n²)，所以 n 必须压在小范围内
 */
const props = withDefaults(
  defineProps<{
    /** 每百万像素多少个点，控制疏密 */
    density?: number
    /** 点数上限。连线是 O(n²)，这个值不能放大 */
    max?: number
    /** 超过这个距离就不连线（像素） */
    linkDistance?: number
    color?: string
  }>(),
  { density: 70, max: 90, linkDistance: 130, color: '255, 255, 255' },
)

const canvas = ref<HTMLCanvasElement>()

let ctx: CanvasRenderingContext2D | null = null
let raf = 0
let particles: { x: number; y: number; vx: number; vy: number; r: number }[] = []
let width = 0
let height = 0
let dpr = 1
let ro: ResizeObserver | null = null
let reduced = false

function seed() {
  const area = (width * height) / 1_000_000
  const count = Math.min(props.max, Math.max(18, Math.round(area * props.density)))
  particles = Array.from({ length: count }, () => ({
    x: Math.random() * width,
    y: Math.random() * height,
    // 速度很慢：这是背景，动得明显就成了干扰
    vx: (Math.random() - 0.5) * 0.22,
    vy: (Math.random() - 0.5) * 0.22,
    r: Math.random() * 1.4 + 0.8,
  }))
}

function resize() {
  const el = canvas.value
  if (!el || !el.parentElement) return
  const rect = el.parentElement.getBoundingClientRect()
  if (rect.width === 0 || rect.height === 0) return

  // 高分屏下按 dpr 放大画布再缩回去，否则点和线是糊的。
  // dpr 封到 2：3 倍屏上像素翻九倍，收益却看不出来。
  dpr = Math.min(window.devicePixelRatio || 1, 2)
  width = rect.width
  height = rect.height
  el.width = Math.round(width * dpr)
  el.height = Math.round(height * dpr)
  el.style.width = `${width}px`
  el.style.height = `${height}px`
  ctx = el.getContext('2d')
  ctx?.setTransform(dpr, 0, 0, dpr, 0, 0)

  seed()
  if (reduced) draw()
}

function draw() {
  if (!ctx) return
  ctx.clearRect(0, 0, width, height)

  // 先连线后画点，点才会压在线头上，接头处干净
  const maxD = props.linkDistance
  const maxD2 = maxD * maxD
  for (let i = 0; i < particles.length; i++) {
    const a = particles[i]
    for (let j = i + 1; j < particles.length; j++) {
      const b = particles[j]
      const dx = a.x - b.x
      const dy = a.y - b.y
      const d2 = dx * dx + dy * dy
      if (d2 > maxD2) continue
      // 开方只在确定要连线时做，省掉大部分计算
      const t = 1 - Math.sqrt(d2) / maxD
      ctx.strokeStyle = `rgba(${props.color}, ${(t * 0.18).toFixed(3)})`
      ctx.lineWidth = 1
      ctx.beginPath()
      ctx.moveTo(a.x, a.y)
      ctx.lineTo(b.x, b.y)
      ctx.stroke()
    }
  }

  ctx.fillStyle = `rgba(${props.color}, 0.5)`
  for (const p of particles) {
    ctx.beginPath()
    ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2)
    ctx.fill()
  }
}

function step() {
  for (const p of particles) {
    p.x += p.vx
    p.y += p.vy
    // 碰到边界反弹而不是穿到对面：穿越会让连线突然横跨整屏，很跳
    if (p.x < 0 || p.x > width) p.vx *= -1
    if (p.y < 0 || p.y > height) p.vy *= -1
    p.x = Math.max(0, Math.min(width, p.x))
    p.y = Math.max(0, Math.min(height, p.y))
  }
  draw()
  raf = requestAnimationFrame(step)
}

function start() {
  if (reduced || raf) return
  raf = requestAnimationFrame(step)
}

function stop() {
  if (raf) cancelAnimationFrame(raf)
  raf = 0
}

function onVisibility() {
  if (document.hidden) stop()
  else start()
}

onMounted(() => {
  reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  resize()
  if (canvas.value?.parentElement) {
    ro = new ResizeObserver(resize)
    ro.observe(canvas.value.parentElement)
  }
  document.addEventListener('visibilitychange', onVisibility)
  start()
})

onBeforeUnmount(() => {
  stop()
  ro?.disconnect()
  document.removeEventListener('visibilitychange', onVisibility)
})
</script>

<template>
  <canvas ref="canvas" class="ly-particles" aria-hidden="true" />
</template>

<style scoped>
.ly-particles {
  position: absolute;
  inset: 0;
  display: block;
  pointer-events: none;
}
</style>
