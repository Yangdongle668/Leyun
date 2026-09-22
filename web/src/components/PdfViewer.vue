<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { ElMessage } from 'element-plus'
// 用 legacy 构建而不是默认构建：默认构建假定浏览器很新，
// 企业内网里成片的旧版 Chrome/Edge 会直接白屏。legacy 版本做过降级转译。
//
// 版本锁死在 5.4.x：6.x 用到了 Map.prototype.getOrInsertComputed，
// 这个 API 连 Chromium 141 都还没有，页面会渲染失败且不报错。
import * as pdfjsLib from 'pdfjs-dist/legacy/build/pdf.mjs'
import type { PDFDocumentProxy } from 'pdfjs-dist'
// 把 worker 作为静态资源打进产物：内网部署拿不到 CDN，不能走外链。
import pdfWorkerUrl from 'pdfjs-dist/legacy/build/pdf.worker.min.mjs?url'

pdfjsLib.GlobalWorkerOptions.workerSrc = pdfWorkerUrl

const props = withDefaults(
  defineProps<{
    /** PDF 文件地址（已带好鉴权参数）。 */
    src: string
    fileName?: string
    /** 无下载权限时不提供下载、打印与文字复制——"可看不可下"要真的成立。 */
    canDownload?: boolean
    /** 有编辑权且系统启用了 ONLYOFFICE 时，提供转到 PDF 编辑器的入口。 */
    canEdit?: boolean
  }>(),
  { fileName: '文档.pdf', canDownload: false, canEdit: false },
)

const emit = defineEmits<{ edit: [] }>()

const scroller = ref<HTMLDivElement>()
const doc = shallowRef<PDFDocumentProxy | null>(null)
const pageCount = ref(0)
const currentPage = ref(1)
const scale = ref(1)
const rotation = ref(0)
const loading = ref(true)
const loadProgress = ref(0)
const error = ref('')
const pageInput = ref('1')

/** 搜索：命中页码列表 + 当前落在第几个命中。 */
const searchOpen = ref(false)
const searchText = ref('')
const searching = ref(false)
const matchPages = ref<number[]>([])
const matchIndex = ref(0)

/** 已经渲染过的页号，避免滚动时重复渲染。 */
const rendered = new Set<number>()
/** 正在渲染中的页号，防止同一页并发渲染把画布画花。 */
const rendering = new Set<number>()
let observer: IntersectionObserver | null = null
let loadingTask: ReturnType<typeof pdfjsLib.getDocument> | null = null

const zoomPercent = computed(() => Math.round(scale.value * 100))
// 没有下载权限就不给文字层：文字层能选中复制，等于把内容带走了。
const textLayerEnabled = computed(() => props.canDownload)

/* ---------------- 加载 ---------------- */

async function load() {
  loading.value = true
  error.value = ''
  loadProgress.value = 0
  try {
    loadingTask = pdfjsLib.getDocument({
      url: props.src,
      // 中日韩文字要靠 cmaps 才能正确显示，缺了就是一屏乱码。
      cMapUrl: '/pdfjs/cmaps/',
      cMapPacked: true,
      // PDF 没内嵌字体时的兜底字形。
      standardFontDataUrl: '/pdfjs/standard_fonts/',
    })
    loadingTask.onProgress = ({ loaded, total }: { loaded: number; total: number }) => {
      if (total > 0) loadProgress.value = Math.min(100, Math.round((loaded / total) * 100))
    }
    const pdf = await loadingTask.promise
    doc.value = pdf
    pageCount.value = pdf.numPages
    currentPage.value = 1
    pageInput.value = '1'
    await nextTick()
    await fitWidth()
    setupObserver()
    scroller.value?.removeEventListener('scroll', onScroll)
    scroller.value?.addEventListener('scroll', onScroll, { passive: true })
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err)
    error.value = /password/i.test(msg)
      ? '该 PDF 已加密，需要口令才能打开'
      : `无法打开该 PDF：${msg}`
  } finally {
    loading.value = false
  }
}

/* ---------------- 渲染 ---------------- */

function pageEl(n: number) {
  return scroller.value?.querySelector<HTMLDivElement>(`[data-page="${n}"]`) ?? null
}

async function renderPage(n: number) {
  const pdf = doc.value
  if (!pdf || rendered.has(n) || rendering.has(n)) return
  const holder = pageEl(n)
  if (!holder) return
  rendering.add(n)
  try {
    const page = await pdf.getPage(n)
    const viewport = page.getViewport({ scale: scale.value, rotation: rotation.value })

    const canvas = holder.querySelector('canvas')
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    // 按设备像素比放大画布，否则高分屏上字会发虚。
    const dpr = Math.min(window.devicePixelRatio || 1, 2)
    canvas.width = Math.floor(viewport.width * dpr)
    canvas.height = Math.floor(viewport.height * dpr)
    canvas.style.width = `${Math.floor(viewport.width)}px`
    canvas.style.height = `${Math.floor(viewport.height)}px`
    holder.style.width = `${Math.floor(viewport.width)}px`
    holder.style.height = `${Math.floor(viewport.height)}px`
    // pdf.js 的文字层靠这两个 CSS 变量定位，少一个就会整体错位。
    holder.style.setProperty('--scale-factor', String(scale.value))
    holder.style.setProperty('--user-unit', '1')

    await page.render({
      canvas,
      canvasContext: ctx,
      viewport,
      transform: dpr === 1 ? undefined : [dpr, 0, 0, dpr, 0, 0],
    }).promise

    const textDiv = holder.querySelector<HTMLDivElement>('.textLayer')
    if (textDiv) {
      textDiv.replaceChildren()
      if (textLayerEnabled.value) {
        const layer = new pdfjsLib.TextLayer({
          textContentSource: await page.getTextContent(),
          container: textDiv,
          viewport,
        })
        await layer.render()
        if (searchText.value) highlight(textDiv, searchText.value)
      }
    }
    rendered.add(n)
  } catch (err) {
    // 单页渲染失败不该让整份文档打不开，记一笔继续。
    console.warn(`第 ${n} 页渲染失败`, err)
  } finally {
    rendering.delete(n)
  }
}

/** 视口内的页才渲染，几百页的 PDF 也不会把内存撑爆。 */
function setupObserver() {
  observer?.disconnect()
  observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return
        const n = Number((entry.target as HTMLElement).dataset.page)
        if (n) void renderPage(n)
      })
    },
    // 提前 400px 开始渲染，滚到跟前时已经画好了。
    { root: scroller.value, rootMargin: '400px 0px' },
  )
  for (let n = 1; n <= pageCount.value; n += 1) {
    const el = pageEl(n)
    if (el) observer.observe(el)
  }
}

/**
 * 按滚动位置判定当前页。
 *
 * 不用 IntersectionObserver 的 ratio 来定：预渲染的 rootMargin 会让多页同时"可见"，
 * 最后回调的那一页会赢，于是明明在看第一页、页码却显示 3。
 * 直接看视口中线落在哪一页上，简单也准确。
 */
function updateCurrentPage() {
  const el = scroller.value
  if (!el || !pageCount.value) return
  const mid = el.scrollTop + el.clientHeight / 2
  let best = 1
  for (let n = 1; n <= pageCount.value; n += 1) {
    const p = pageEl(n)
    if (!p) continue
    if (p.offsetTop <= mid) best = n
    else break
  }
  if (best !== currentPage.value) {
    currentPage.value = best
    pageInput.value = String(best)
  }
}

let scrollTick = 0
function onScroll() {
  // 滚动事件很密，用 rAF 合并，避免每帧都遍历一遍页面。
  if (scrollTick) return
  scrollTick = requestAnimationFrame(() => {
    scrollTick = 0
    updateCurrentPage()
  })
}

/** 缩放或旋转后，所有页都要重画。 */
async function rerenderAll() {
  rendered.clear()
  await nextTick()
  const visible = currentPage.value
  for (let n = Math.max(1, visible - 1); n <= Math.min(pageCount.value, visible + 2); n += 1) {
    await renderPage(n)
  }
  setupObserver()
}

/* ---------------- 工具栏 ---------------- */

function zoom(delta: number) {
  scale.value = Math.min(4, Math.max(0.25, Math.round((scale.value + delta) * 100) / 100))
}

async function fitWidth() {
  const pdf = doc.value
  if (!pdf || !scroller.value) return
  const page = await pdf.getPage(currentPage.value)
  const base = page.getViewport({ scale: 1, rotation: rotation.value })
  // 减去左右留白与滚动条，避免算出来刚好横向溢出。
  const avail = scroller.value.clientWidth - 48
  scale.value = Math.min(4, Math.max(0.25, Math.round((avail / base.width) * 100) / 100))
}

function rotate() {
  rotation.value = (rotation.value + 90) % 360
}

function goPage(n: number) {
  const target = Math.min(pageCount.value, Math.max(1, n))
  currentPage.value = target
  pageInput.value = String(target)
  pageEl(target)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function onPageInput() {
  const n = Number(pageInput.value)
  if (Number.isFinite(n)) goPage(n)
  else pageInput.value = String(currentPage.value)
}

function download() {
  if (!props.canDownload) return
  const a = document.createElement('a')
  a.href = props.src
  a.download = props.fileName
  a.click()
}

function print() {
  if (!props.canDownload) return
  // 交给浏览器自带的 PDF 打印，比自己拼图片保真。
  const win = window.open(props.src, '_blank')
  if (!win) ElMessage.warning('浏览器拦截了弹窗，请允许后重试')
}

/* ---------------- 搜索 ---------------- */

function highlight(container: HTMLElement, keyword: string) {
  const needle = keyword.toLowerCase()
  container.querySelectorAll('span').forEach((span) => {
    if ((span.textContent || '').toLowerCase().includes(needle)) {
      span.classList.add('ly-pdf-hit')
    } else {
      span.classList.remove('ly-pdf-hit')
    }
  })
}

async function runSearch() {
  const pdf = doc.value
  const keyword = searchText.value.trim()
  if (!pdf || !keyword) {
    matchPages.value = []
    return
  }
  searching.value = true
  try {
    const needle = keyword.toLowerCase()
    const hits: number[] = []
    for (let n = 1; n <= pdf.numPages; n += 1) {
      const page = await pdf.getPage(n)
      const content = await page.getTextContent()
      const text = content.items
        .map((item) => ('str' in item ? item.str : ''))
        .join('')
        .toLowerCase()
      if (text.includes(needle)) hits.push(n)
    }
    matchPages.value = hits
    matchIndex.value = 0
    if (!hits.length) {
      ElMessage.info('没有找到匹配的内容')
      return
    }
    // 重新渲染已画过的页，让高亮生效。
    rendered.clear()
    goPage(hits[0])
    await rerenderAll()
  } finally {
    searching.value = false
  }
}

function stepMatch(delta: number) {
  if (!matchPages.value.length) return
  matchIndex.value =
    (matchIndex.value + delta + matchPages.value.length) % matchPages.value.length
  goPage(matchPages.value[matchIndex.value])
}

function closeSearch() {
  searchOpen.value = false
  searchText.value = ''
  matchPages.value = []
  rendered.clear()
  void rerenderAll()
}

/* ---------------- 生命周期 ---------------- */

watch(() => props.src, load)
watch([scale, rotation], rerenderAll)
watch(textLayerEnabled, rerenderAll)

onMounted(load)

onBeforeUnmount(() => {
  observer?.disconnect()
  scroller.value?.removeEventListener('scroll', onScroll)
  if (scrollTick) cancelAnimationFrame(scrollTick)
  // 不销毁的话 worker 会一直挂着，连开几个 PDF 后内存涨得很快。
  // 销毁 loadingTask 会连带释放它产出的 document 与 worker。
  void loadingTask?.destroy()
})

defineExpose({ reload: load })
</script>

<template>
  <div class="ly-pdf">
    <!-- 工具栏 -->
    <div class="ly-pdf-bar">
      <div class="ly-pdf-group">
        <button class="ly-pdf-btn" title="上一页" :disabled="currentPage <= 1" @click="goPage(currentPage - 1)">
          <el-icon><ArrowUp /></el-icon>
        </button>
        <input
          v-model="pageInput"
          class="ly-pdf-page-input"
          inputmode="numeric"
          @keyup.enter="onPageInput"
          @blur="onPageInput"
        />
        <span class="ly-pdf-page-total">/ {{ pageCount || '—' }}</span>
        <button
          class="ly-pdf-btn"
          title="下一页"
          :disabled="currentPage >= pageCount"
          @click="goPage(currentPage + 1)"
        >
          <el-icon><ArrowDown /></el-icon>
        </button>
      </div>

      <div class="ly-pdf-divider"></div>

      <div class="ly-pdf-group">
        <button class="ly-pdf-btn" title="缩小" @click="zoom(-0.15)">
          <el-icon><ZoomOut /></el-icon>
        </button>
        <span class="ly-pdf-zoom">{{ zoomPercent }}%</span>
        <button class="ly-pdf-btn" title="放大" @click="zoom(0.15)">
          <el-icon><ZoomIn /></el-icon>
        </button>
        <button class="ly-pdf-btn" title="适应宽度" @click="fitWidth">
          <el-icon><FullScreen /></el-icon>
        </button>
        <button class="ly-pdf-btn" title="旋转 90°" @click="rotate">
          <el-icon><RefreshRight /></el-icon>
        </button>
      </div>

      <div v-if="textLayerEnabled" class="ly-pdf-divider"></div>
      <div v-if="textLayerEnabled" class="ly-pdf-group">
        <button
          v-if="!searchOpen"
          class="ly-pdf-btn"
          title="在文档中搜索"
          @click="searchOpen = true"
        >
          <el-icon><Search /></el-icon>
        </button>
        <template v-else>
          <input
            v-model="searchText"
            class="ly-pdf-search"
            placeholder="搜索内容"
            autofocus
            @keyup.enter="runSearch"
          />
          <button class="ly-pdf-btn" title="搜索" :disabled="searching" @click="runSearch">
            <el-icon><Search /></el-icon>
          </button>
          <span v-if="matchPages.length" class="ly-pdf-hits">
            第 {{ matchIndex + 1 }} / {{ matchPages.length }} 页
          </span>
          <button class="ly-pdf-btn" title="上一处" @click="stepMatch(-1)">
            <el-icon><ArrowLeft /></el-icon>
          </button>
          <button class="ly-pdf-btn" title="下一处" @click="stepMatch(1)">
            <el-icon><ArrowRight /></el-icon>
          </button>
          <button class="ly-pdf-btn" title="关闭搜索" @click="closeSearch">
            <el-icon><Close /></el-icon>
          </button>
        </template>
      </div>

      <span class="ly-spacer"></span>

      <div class="ly-pdf-group">
        <el-button v-if="canEdit" size="small" type="primary" :icon="'EditPen'" @click="emit('edit')">
          在线编辑
        </el-button>
        <button v-if="canDownload" class="ly-pdf-btn" title="打印" @click="print">
          <el-icon><Printer /></el-icon>
        </button>
        <button v-if="canDownload" class="ly-pdf-btn" title="下载" @click="download">
          <el-icon><Download /></el-icon>
        </button>
        <span v-if="!canDownload" class="ly-pdf-notice" title="你在此处只有查看权限">
          <el-icon><Lock /></el-icon> 仅查看
        </span>
      </div>
    </div>

    <!-- 页面区 -->
    <div ref="scroller" class="ly-pdf-body">
      <div v-if="loading" class="ly-pdf-state">
        <el-icon class="is-spin" :size="26"><Loading /></el-icon>
        <p>正在加载文档{{ loadProgress ? ` ${loadProgress}%` : '' }}…</p>
      </div>
      <div v-else-if="error" class="ly-pdf-state">
        <el-icon :size="32"><WarningFilled /></el-icon>
        <p>{{ error }}</p>
      </div>
      <template v-else>
        <div v-for="n in pageCount" :key="n" class="ly-pdf-page" :data-page="n">
          <canvas></canvas>
          <div class="textLayer"></div>
          <span class="ly-pdf-page-no">{{ n }}</span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.ly-pdf {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: var(--ly-surface-sunken);
  border-radius: var(--ly-radius);
  overflow: hidden;
}

/* ---------- 工具栏 ---------- */
.ly-pdf-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 8px 12px;
  background: var(--ly-surface);
  border-bottom: 1px solid var(--ly-border);
  flex-shrink: 0;
}
.ly-pdf-group {
  display: flex;
  align-items: center;
  gap: 4px;
}
.ly-pdf-divider {
  width: 1px;
  height: 18px;
  background: var(--ly-border);
}
.ly-pdf-btn {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--ly-text-secondary);
  cursor: pointer;
  font-size: 15px;
}
.ly-pdf-btn:hover:not(:disabled) {
  background: var(--ly-primary-soft);
  color: var(--ly-primary);
}
.ly-pdf-btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
.ly-pdf-page-input {
  width: 44px;
  height: 26px;
  text-align: center;
  border: 1px solid var(--ly-border-strong);
  border-radius: 6px;
  font-size: 13px;
  font-family: inherit;
  color: var(--ly-text);
  background: var(--ly-surface);
}
.ly-pdf-page-input:focus {
  outline: none;
  border-color: var(--ly-primary);
}
.ly-pdf-page-total,
.ly-pdf-zoom,
.ly-pdf-hits {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  white-space: nowrap;
}
.ly-pdf-zoom {
  min-width: 42px;
  text-align: center;
}
.ly-pdf-search {
  width: 150px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--ly-border-strong);
  border-radius: 6px;
  font-size: 13px;
  font-family: inherit;
  background: var(--ly-surface);
}
.ly-pdf-search:focus {
  outline: none;
  border-color: var(--ly-primary);
}
.ly-pdf-notice {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--ly-text-tertiary);
  padding: 0 4px;
}

/* ---------- 页面区 ---------- */
.ly-pdf-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 16px 0 24px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
}
.ly-pdf-page {
  position: relative;
  background: #fff;
  box-shadow: var(--ly-shadow-sm);
  /* 未渲染时先占个位，滚动条长度才不会跳来跳去 */
  min-height: 120px;
  min-width: 120px;
  /*
   * 关键：这里是 column flex 的子项，flex-shrink 默认为 1。
   * 不关掉的话，明明把高度设成了一整页，仍会被压缩到容器高度内，
   * 后一页就盖到前一页的画布上，看起来像每页只剩顶上一条。
   */
  flex-shrink: 0;
  overflow: hidden;
}
.ly-pdf-page canvas {
  display: block;
}
.ly-pdf-page-no {
  position: absolute;
  right: 8px;
  bottom: 6px;
  font-size: 11px;
  color: var(--ly-text-tertiary);
  background: rgba(255, 255, 255, 0.82);
  border-radius: 4px;
  padding: 0 5px;
  pointer-events: none;
}

.ly-pdf-state {
  margin: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  color: var(--ly-text-tertiary);
  font-size: 13px;
}
.ly-pdf-state p {
  margin: 0;
  max-width: 460px;
  text-align: center;
  line-height: 1.8;
}
.is-spin {
  animation: ly-pdf-spin 1s linear infinite;
}
@keyframes ly-pdf-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>

<style>
/*
 * pdf.js 文字层。
 *
 * 这几条规则从 pdfjs-dist/web/pdf_viewer.css 里摘出来——整份 160KB 的
 * 官方样式里绝大部分是它自带那套阅读器界面的，我们只用文字层这一块。
 * 不能加 scoped：这些节点是 pdf.js 在运行时插进来的，带不上作用域属性。
 */
.ly-pdf .textLayer {
  color-scheme: only light;
  position: absolute;
  text-align: initial;
  inset: 0;
  overflow: clip;
  opacity: 1;
  line-height: 1;
  letter-spacing: normal;
  word-spacing: normal;
  text-size-adjust: none;
  forced-color-adjust: none;
  transform-origin: 0 0;
  caret-color: CanvasText;
  z-index: 0;
  --total-scale-factor: calc(var(--scale-factor, 1) * var(--user-unit, 1));
  --min-font-size: 1;
  --text-scale-factor: calc(var(--total-scale-factor) * var(--min-font-size));
  --min-font-size-inv: calc(1 / var(--min-font-size));
}
.ly-pdf .textLayer :is(span, br) {
  color: transparent;
  position: absolute;
  white-space: pre;
  cursor: text;
  transform-origin: 0% 0%;
  user-select: text;
}
.ly-pdf .textLayer > :not(.markedContent),
.ly-pdf .textLayer .markedContent span:not(.markedContent) {
  z-index: 1;
  --font-height: 0;
  font-size: calc(var(--text-scale-factor) * var(--font-height));
  --scale-x: 1;
  --rotate: 0deg;
  transform: rotate(var(--rotate)) scaleX(var(--scale-x)) scale(var(--min-font-size-inv));
}
.ly-pdf .textLayer .markedContent {
  display: contents;
}
.ly-pdf .textLayer ::selection {
  background: rgba(31, 94, 255, 0.3);
}
.ly-pdf .textLayer .ly-pdf-hit {
  background: rgba(245, 165, 36, 0.42);
  border-radius: 2px;
}
</style>
