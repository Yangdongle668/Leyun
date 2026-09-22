<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { authedURL } from '@/api'
import type { FileNode } from '@/api/types'
import { extOf } from '@/utils/format'

// pdf.js 体积不小，按需加载：不看 PDF 的人不必为它付首屏代价。
const PdfViewer = defineAsyncComponent(() => import('@/components/PdfViewer.vue'))

const props = defineProps<{
  modelValue: boolean
  spaceId: number
  node: FileNode | null
  /** 分享页预览走另一套无需登录的地址。 */
  shareCode?: string
  sharePassword?: string
  /** 无下载权限时，PDF 阅读器不提供下载、打印与文字复制。 */
  canDownload?: boolean
  /** 有编辑权且启用了 ONLYOFFICE 时，PDF 阅读器里给出转编辑器的入口。 */
  canEditPdf?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [boolean]; 'edit-pdf': [FileNode] }>()

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const textContent = ref('')
const textLoading = ref(false)

const src = computed(() => {
  if (!props.node) return ''
  if (props.shareCode) {
    const url = new URL(`/api/v1/share/${props.shareCode}/preview`, window.location.origin)
    url.searchParams.set('node_id', String(props.node.id))
    if (props.sharePassword) url.searchParams.set('password', props.sharePassword)
    return url.toString()
  }
  return authedURL(`/files/${props.node.id}/preview`, { space_id: props.spaceId })
})

type Kind = 'image' | 'video' | 'audio' | 'pdf' | 'text' | 'none'

const kind = computed<Kind>(() => {
  if (!props.node) return 'none'
  const ext = extOf(props.node.name)
  const mime = props.node.mime_type || ''
  if (mime.startsWith('image/') || ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg'].includes(ext))
    return 'image'
  if (mime.startsWith('video/') || ['mp4', 'webm', 'ogv', 'mov'].includes(ext)) return 'video'
  if (mime.startsWith('audio/') || ['mp3', 'wav', 'm4a', 'ogg', 'flac'].includes(ext)) return 'audio'
  if (ext === 'pdf') return 'pdf'
  if (
    mime.startsWith('text/') ||
    ['txt', 'md', 'log', 'json', 'xml', 'yml', 'yaml', 'csv', 'go', 'js', 'ts', 'py', 'java', 'c', 'cpp', 'h', 'sh', 'sql', 'css', 'html'].includes(ext)
  )
    return 'text'
  return 'none'
})

watch(
  () => [visible.value, src.value, kind.value] as const,
  async ([open, url, k]) => {
    textContent.value = ''
    if (!open || k !== 'text' || !url) return
    textLoading.value = true
    try {
      const resp = await fetch(url)
      // 纯文本预览截断到 512KB，避免几十兆的日志把浏览器拖死。
      const blob = await resp.blob()
      const slice = blob.slice(0, 512 * 1024)
      textContent.value = await slice.text()
      if (blob.size > slice.size) {
        textContent.value += '\n\n…（文件过大，仅显示前 512 KB）'
      }
    } catch {
      textContent.value = '（无法加载预览内容）'
    } finally {
      textLoading.value = false
    }
  },
  { immediate: false },
)
</script>

<template>
  <el-dialog v-model="visible" width="min(1080px, 92vw)" top="5vh" destroy-on-close class="ly-preview-dlg">
    <template #header>
      <div class="ly-dlg-head">
        <span>{{ node?.name }}</span>
        <span class="ly-dlg-sub">{{ node?.size_text }}</span>
      </div>
    </template>

    <!--
      PDF 自己管滚动。外层再套一个滚动容器会形成嵌套滚动：
      scrollIntoView 可能滚的是外层，翻页就失灵了。
    -->
    <div class="ly-preview-body" :class="{ 'is-pdf': kind === 'pdf' }">
      <img v-if="kind === 'image'" :src="src" :alt="node?.name" class="ly-preview-img" />
      <video v-else-if="kind === 'video'" :src="src" controls class="ly-preview-video" />
      <audio v-else-if="kind === 'audio'" :src="src" controls class="ly-preview-audio" />
      <!--
        PDF 走自带的 PDF.js 阅读器，不用 <iframe> 套浏览器内置阅读器：
        后者自带下载与打印按钮，会把"可看不可下"的权限设定直接绕过去。
      -->
      <PdfViewer
        v-else-if="kind === 'pdf'"
        :key="src"
        :src="src"
        :file-name="node?.name"
        :can-download="canDownload"
        :can-edit="canEditPdf"
        class="ly-preview-pdf"
        @edit="node && emit('edit-pdf', node)"
      />
      <pre v-else-if="kind === 'text'" v-loading="textLoading" class="ly-preview-text">{{ textContent }}</pre>
      <div v-else class="ly-empty">
        <el-icon :size="40" class="ly-muted"><Document /></el-icon>
        <p>该文件类型不支持在线预览，请下载后查看</p>
      </div>
    </div>
  </el-dialog>
</template>

<style scoped>
.ly-dlg-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  min-width: 0;
}
.ly-dlg-sub {
  font-size: 13px;
  font-weight: 400;
  color: var(--ly-text-tertiary);
  flex-shrink: 0;
}

.ly-preview-body {
  min-height: 320px;
  max-height: 76vh;
  overflow: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--ly-surface-sunken);
  border-radius: var(--ly-radius);
}
.ly-preview-body.is-pdf {
  overflow: hidden;
  height: 76vh;
  align-items: stretch;
}
.ly-preview-img {
  max-width: 100%;
  max-height: 74vh;
  object-fit: contain;
  display: block;
}
.ly-preview-video {
  max-width: 100%;
  max-height: 74vh;
  background: #000;
}
.ly-preview-audio {
  width: 80%;
}
.ly-preview-pdf {
  width: 100%;
  height: 100%;
  min-height: 0;
}
.ly-preview-text {
  width: 100%;
  margin: 0;
  padding: 18px 20px;
  align-self: stretch;
  font-family: 'SFMono-Regular', Consolas, 'Liberation Mono', Menlo, monospace;
  font-size: 12.5px;
  line-height: 1.75;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--ly-text);
  background: var(--ly-surface);
  border-radius: var(--ly-radius);
}
</style>
