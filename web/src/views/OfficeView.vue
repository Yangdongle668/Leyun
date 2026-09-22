<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '@/api'

const route = useRoute()
const router = useRouter()

const spaceId = Number(route.params.spaceId)
const nodeId = Number(route.params.nodeId)

const loading = ref(true)
const error = ref('')
const fileName = ref('')
const mode = ref<'edit' | 'view'>('view')
const docType = ref('')
const container = ref<HTMLDivElement>()

const modeLabel = computed(() => {
  if (mode.value !== 'edit') return '只读预览'
  // PDF 编辑器的能力和 Office 文档不一样，文案分开说更准确。
  return docType.value === 'pdf' ? 'PDF 编辑' : '编辑模式'
})

// ONLYOFFICE 的 api.js 会往 window 上挂 DocsAPI。
declare global {
  interface Window {
    DocsAPI?: {
      DocEditor: new (id: string, config: Record<string, unknown>) => { destroyEditor: () => void }
    }
  }
}

let editor: { destroyEditor: () => void } | null = null

/** 加载 Document Server 的 api.js；已加载过就直接复用。 */
function loadScript(serverURL: string): Promise<void> {
  if (window.DocsAPI) return Promise.resolve()
  return new Promise((resolve, reject) => {
    const script = document.createElement('script')
    script.src = `${serverURL}/web-apps/apps/api/documents/api.js`
    script.async = true
    script.onload = () => resolve()
    script.onerror = () =>
      reject(new Error('无法加载在线编辑器，请检查 Document Server 地址是否可从浏览器访问'))
    document.head.appendChild(script)
  })
}

onMounted(async () => {
  try {
    const cfg = await api.officeConfig(spaceId, nodeId)
    fileName.value = cfg.file_name
    mode.value = cfg.mode
    docType.value = cfg.doc_type
    await loadScript(cfg.server_url)
    if (!window.DocsAPI) throw new Error('在线编辑器未正确加载')

    const config = { ...cfg.config } as Record<string, unknown>
    // 编辑器关闭时回到文件列表，而不是停在空白页。
    config.events = {
      onRequestClose: () => back(),
      onError: (e: { data?: { errorDescription?: string } }) => {
        error.value = e?.data?.errorDescription || '在线编辑器报错'
      },
    }
    editor = new window.DocsAPI.DocEditor('ly-office-holder', config)
  } catch (err) {
    error.value = err instanceof Error ? err.message : '打开在线编辑器失败'
  } finally {
    loading.value = false
  }
})

onBeforeUnmount(() => {
  // 不销毁的话，返回列表再进来会叠出多个编辑器实例。
  try {
    editor?.destroyEditor()
  } catch {
    /* 编辑器已自行销毁 */
  }
})

function back() {
  router.push({ name: 'files', params: { spaceId: String(spaceId) } })
}
</script>

<template>
  <div class="ly-office">
    <header class="ly-office-bar">
      <button class="ly-office-back" @click="back">
        <el-icon><ArrowLeft /></el-icon>
        返回文件
      </button>
      <span class="ly-office-name">{{ fileName }}</span>
      <span class="ly-tag" :class="mode === 'edit' ? 'ly-tag--primary' : ''">
        {{ modeLabel }}
      </span>
      <span class="ly-spacer"></span>
      <span v-if="mode === 'edit'" class="ly-office-hint">改动会自动保存回乐云</span>
    </header>

    <div v-if="error" class="ly-office-error">
      <el-icon :size="40"><WarningFilled /></el-icon>
      <p>{{ error }}</p>
      <el-button @click="back">返回文件列表</el-button>
    </div>

    <div v-else v-loading="loading" element-loading-text="正在打开文档…" class="ly-office-body">
      <div id="ly-office-holder" ref="container" class="ly-office-holder"></div>
    </div>
  </div>
</template>

<style scoped>
.ly-office {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--ly-bg);
}

.ly-office-bar {
  height: 52px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 18px;
  background: var(--ly-surface);
  border-bottom: 1px solid var(--ly-border);
}
.ly-office-back {
  display: flex;
  align-items: center;
  gap: 5px;
  height: 32px;
  padding: 0 12px 0 8px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--ly-text-secondary);
  font-family: inherit;
  font-size: 13px;
  cursor: pointer;
}
.ly-office-back:hover {
  background: var(--ly-surface-sunken);
  color: var(--ly-text);
}
.ly-office-name {
  font-size: 14px;
  font-weight: 500;
  max-width: 460px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-office-hint {
  font-size: 12px;
  color: var(--ly-text-tertiary);
}

.ly-office-body {
  flex: 1;
  min-height: 0;
}
.ly-office-holder {
  width: 100%;
  height: 100%;
}

.ly-office-error {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  color: var(--ly-text-tertiary);
}
.ly-office-error p {
  margin: 0;
  max-width: 520px;
  text-align: center;
  line-height: 1.8;
}
</style>
