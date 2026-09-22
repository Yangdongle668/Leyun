<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, authedURL } from '@/api'
import type { Crumb, FileNode, ListResult, PermCode } from '@/api/types'
import { useUserStore } from '@/stores/user'
import { humanSize, relativeTime, spaceTypeLabel } from '@/utils/format'
import { filesFromDataTransfer, runUpload, type UploadTask } from '@/utils/upload'
import FileIcon from '@/components/FileIcon.vue'
import UploadPanel from '@/components/UploadPanel.vue'
import PermissionDialog from '@/components/PermissionDialog.vue'
import ShareDialog from '@/components/ShareDialog.vue'
import MoveDialog from '@/components/MoveDialog.vue'
import PreviewDialog from '@/components/PreviewDialog.vue'

const route = useRoute()
const router = useRouter()
const store = useUserStore()

const spaceId = computed(() => Number(route.params.spaceId) || store.personalSpace?.id || 0)
const parentId = ref(0)
const keyword = ref('')
const orderBy = ref('name')
const loading = ref(false)
const data = ref<ListResult | null>(null)
const selected = ref<FileNode[]>([])
const dragOver = ref(false)
const fileInput = ref<HTMLInputElement>()
const folderInput = ref<HTMLInputElement>()

const tasks = ref<UploadTask[]>([])
const permDialog = ref(false)
const permTarget = ref<{ nodeId: number; title: string }>({ nodeId: 0, title: '' })
const shareDialog = ref(false)
const shareNode = ref<FileNode | null>(null)
const moveDialog = ref(false)
const moveMode = ref<'move' | 'copy'>('move')
const previewDialog = ref(false)
const previewNode = ref<FileNode | null>(null)

const space = computed(() => data.value?.space)
const items = computed(() => data.value?.items ?? [])
const crumbs = computed<Crumb[]>(() => data.value?.crumbs ?? [])
const parentPerms = computed<PermCode[]>(() => data.value?.parent_perms ?? [])

const can = (p: PermCode) => parentPerms.value.includes(p)
const canUpload = computed(() => can('upload'))
const canManage = computed(() => can('manage'))

const selectedIds = computed(() => selected.value.map((n) => n.id))
const hasSelection = computed(() => selected.value.length > 0)

async function load() {
  if (!spaceId.value) {
    // 首次进入且路由没带空间时，落到个人空间。
    await store.refreshSpaces()
    const fallback = store.personalSpace ?? store.spaces[0]
    if (fallback) {
      router.replace({ name: 'files', params: { spaceId: String(fallback.id) } })
    }
    return
  }
  loading.value = true
  try {
    data.value = await api.listFiles({
      space_id: spaceId.value,
      parent_id: parentId.value,
      keyword: keyword.value.trim() || undefined,
      order_by: orderBy.value,
    })
    selected.value = []
  } finally {
    loading.value = false
  }
}

onMounted(load)

watch(
  () => route.params.spaceId,
  () => {
    parentId.value = 0
    keyword.value = ''
    load()
  },
)
watch(orderBy, load)

function openNode(node: FileNode) {
  if (node.is_dir) {
    parentId.value = node.id
    keyword.value = ''
    load()
    return
  }
  // PDF 默认走轻量的内置阅读器：秒开、不依赖 Document Server；
  // 要改内容或填表单再从阅读器里转到 Office 编辑器。
  if (node.is_pdf) {
    previewNode.value = node
    previewDialog.value = true
    return
  }
  if (node.editable && store.officeEnabled) {
    openInOffice(node)
    return
  }
  if (node.previewable) {
    previewNode.value = node
    previewDialog.value = true
    return
  }
  download(node)
}

function openInOffice(node: FileNode) {
  router.push({ name: 'office', params: { spaceId: String(spaceId.value), nodeId: String(node.id) } })
}

/** 当前预览的文件能否转去 Office 编辑（PDF 编辑需要 ONLYOFFICE 8.1+）。 */
const previewCanEditPdf = computed(
  () => !!previewNode.value?.is_pdf && store.pdfEditEnabled && !!previewNode.value?.perms.includes('edit'),
)
const previewCanDownload = computed(() => !!previewNode.value?.perms.includes('download'))

function goCrumb(id: number) {
  parentId.value = id
  keyword.value = ''
  load()
}

function download(node: FileNode) {
  window.location.href = authedURL(`/files/${node.id}/download`, { space_id: spaceId.value })
}

function downloadSelected() {
  selected.value.forEach((n, i) => {
    // 浏览器会拦截密集的连续下载，错开一点更稳。
    setTimeout(() => download(n), i * 350)
  })
}

async function createFolder() {
  const { value } = await ElMessageBox.prompt('请输入目录名称', '新建目录', {
    confirmButtonText: '创建',
    cancelButtonText: '取消',
    inputPattern: /^[^/\\]{1,200}$/,
    inputErrorMessage: '名称不能为空且不能包含斜杠',
  })
  await api.mkdir(spaceId.value, parentId.value, value)
  ElMessage.success('目录已创建')
  await load()
}

async function rename(node: FileNode) {
  const { value } = await ElMessageBox.prompt('请输入新名称', '重命名', {
    confirmButtonText: '保存',
    cancelButtonText: '取消',
    inputValue: node.name,
    inputPattern: /^[^/\\]{1,200}$/,
    inputErrorMessage: '名称不能为空且不能包含斜杠',
  })
  await api.rename(spaceId.value, node.id, value)
  ElMessage.success('已重命名')
  await load()
}

async function removeNodes(nodes: FileNode[]) {
  if (!nodes.length) return
  const label = nodes.length === 1 ? `「${nodes[0].name}」` : `这 ${nodes.length} 个条目`
  await ElMessageBox.confirm(`确定要删除${label}吗？删除后可在回收站中找回。`, '移入回收站', {
    type: 'warning',
    confirmButtonText: '删除',
    cancelButtonText: '取消',
  })
  const res = await api.trash(spaceId.value, nodes.map((n) => n.id))
  ElMessage.success(`已删除 ${res.trashed} 个条目`)
  await Promise.all([load(), store.refreshSpaces()])
}

function openPerm(node: FileNode | null) {
  permTarget.value = node
    ? { nodeId: node.id, title: node.name }
    : { nodeId: parentId.value, title: currentLocationName.value }
  permDialog.value = true
}

const currentLocationName = computed(() => {
  const last = crumbs.value[crumbs.value.length - 1]
  return last ? `${space.value?.name ?? ''} / ${last.name}` : (space.value?.name ?? '')
})

function openShare(node: FileNode) {
  shareNode.value = node
  shareDialog.value = true
}

function openMove(mode: 'move' | 'copy') {
  moveMode.value = mode
  moveDialog.value = true
}

function onRowCommand(cmd: string, node: FileNode) {
  switch (cmd) {
    case 'open':
      openNode(node)
      break
    case 'download':
      download(node)
      break
    case 'rename':
      rename(node)
      break
    case 'share':
      openShare(node)
      break
    case 'perm':
      openPerm(node)
      break
    case 'delete':
      removeNodes([node])
      break
    case 'office':
      openInOffice(node)
      break
    case 'preview':
      previewNode.value = node
      previewDialog.value = true
      break
  }
}

/* ---------------- 上传 ---------------- */

function pickFiles() {
  fileInput.value?.click()
}
function pickFolder() {
  folderInput.value?.click()
}

async function onFilePicked(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files || [])
  const entries = files.map((f) => ({
    file: f,
    // webkitRelativePath 只有选目录时才有值。
    path: (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name,
  }))
  input.value = ''
  await enqueue(entries)
}

async function onDrop(e: DragEvent) {
  dragOver.value = false
  if (!canUpload.value) {
    ElMessage.warning('你在当前目录没有上传权限')
    return
  }
  if (!e.dataTransfer) return
  const entries = await filesFromDataTransfer(e.dataTransfer)
  await enqueue(entries)
}

/** 把拖入/选中的文件排进上传队列；带子目录的会先把目录建出来。 */
async function enqueue(entries: Array<{ file: File; path: string }>) {
  if (!entries.length) return
  if (!canUpload.value) {
    ElMessage.warning('你在当前目录没有上传权限')
    return
  }

  // 目录缓存：同一批文件里相同的子目录只建一次。
  const dirCache = new Map<string, number>([['', parentId.value]])
  const ensureDir = async (dirPath: string): Promise<number> => {
    if (dirCache.has(dirPath)) return dirCache.get(dirPath)!
    const idx = dirPath.lastIndexOf('/')
    const parentPath = idx < 0 ? '' : dirPath.slice(0, idx)
    const name = idx < 0 ? dirPath : dirPath.slice(idx + 1)
    const parent = await ensureDir(parentPath)
    let id: number
    try {
      const node = await api.mkdir(spaceId.value, parent, name)
      id = node.id
    } catch {
      // 目录已存在时再列一次拿到它的 ID。
      const listed = await api.listFiles({ space_id: spaceId.value, parent_id: parent })
      const hit = listed.items.find((n) => n.is_dir && n.name === name)
      if (!hit) throw new Error(`无法创建目录 ${name}`)
      id = hit.id
    }
    dirCache.set(dirPath, id)
    return id
  }

  for (const entry of entries) {
    const idx = entry.path.lastIndexOf('/')
    const dirPath = idx < 0 ? '' : entry.path.slice(0, idx)
    let targetParent = parentId.value
    if (dirPath) {
      try {
        targetParent = await ensureDir(dirPath)
      } catch (err) {
        ElMessage.error(err instanceof Error ? err.message : '创建目录失败')
        continue
      }
    }
    const task: UploadTask = {
      id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      file: entry.file,
      name: entry.file.name,
      size: entry.file.size,
      spaceId: spaceId.value,
      parentId: targetParent,
      progress: 0,
      status: 'hashing',
    }
    tasks.value.push(task)
    void startTask(task)
  }
}

async function startTask(task: UploadTask) {
  try {
    await runUpload(task, () => {
      tasks.value = [...tasks.value]
    })
  } catch (err) {
    task.status = 'error'
    task.message = err instanceof Error ? err.message : '上传失败'
    tasks.value = [...tasks.value]
    return
  }
  // 全部结束后统一刷新一次，避免每个文件都触发一轮请求。
  if (!tasks.value.some((t) => t.status === 'uploading' || t.status === 'hashing')) {
    await Promise.all([load(), store.refreshSpaces()])
  }
}

function cancelTask(task: UploadTask) {
  task.controller?.abort()
  task.status = 'canceled'
  tasks.value = [...tasks.value]
}

function clearTasks() {
  tasks.value = tasks.value.filter((t) => t.status === 'uploading' || t.status === 'hashing')
}
</script>

<template>
  <div
    class="ly-page ly-files"
    :class="{ 'is-drag': dragOver }"
    @dragover.prevent="dragOver = true"
    @dragleave.self="dragOver = false"
    @drop.prevent="onDrop"
  >
    <input ref="fileInput" type="file" multiple hidden @change="onFilePicked" />
    <input
      ref="folderInput"
      type="file"
      multiple
      hidden
      webkitdirectory
      directory
      @change="onFilePicked"
    />

    <!-- 头部：空间名 + 权限概览 -->
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">
          {{ space?.name || '文件' }}
          <span v-if="space" class="ly-tag" style="margin-left: 8px; vertical-align: middle">
            {{ spaceTypeLabel(space.type) }}
          </span>
        </h1>
        <p class="ly-page-desc">
          <template v-if="space">
            已用 {{ humanSize(space.used_bytes) }}
            <template v-if="space.quota_bytes"> / {{ humanSize(space.quota_bytes) }}</template>
            <template v-else> · 不限容量</template>
            · 你在此处可以：{{ parentPerms.length ? parentPerms.map((p) => ({ view: '查看', download: '下载', upload: '上传', edit: '编辑', delete: '删除', share: '分享', manage: '授权' } as Record<string, string>)[p]).join('、') : '暂无权限' }}
          </template>
        </p>
      </div>

      <div class="ly-toolbar">
        <el-button v-if="canUpload" type="primary" :icon="'Upload'" @click="pickFiles">上传文件</el-button>
        <el-button v-if="canUpload" :icon="'FolderAdd'" @click="pickFolder">上传文件夹</el-button>
        <el-button v-if="canUpload" :icon="'FolderAdd'" @click="createFolder">新建目录</el-button>
        <el-button v-if="canManage" :icon="'Key'" @click="openPerm(null)">权限设置</el-button>
      </div>
    </div>

    <div class="ly-card">
      <!-- 工具条：面包屑 + 搜索 + 排序 -->
      <div class="ly-files-bar">
        <el-breadcrumb separator="/">
          <el-breadcrumb-item v-for="c in crumbs" :key="c.id">
            <a href="javascript:void(0)" @click="goCrumb(c.id)">{{ c.name }}</a>
          </el-breadcrumb-item>
        </el-breadcrumb>

        <span class="ly-spacer"></span>

        <el-input
          v-model="keyword"
          placeholder="在当前目录下搜索"
          :prefix-icon="'Search'"
          clearable
          style="width: 220px"
          @keyup.enter="load"
          @clear="load"
        />
        <el-select v-model="orderBy" style="width: 130px">
          <el-option label="名称 ↑" value="name" />
          <el-option label="名称 ↓" value="name_desc" />
          <el-option label="最近修改" value="time_desc" />
          <el-option label="大小 ↓" value="size_desc" />
        </el-select>
      </div>

      <!-- 选中后的批量操作条 -->
      <div v-if="hasSelection" class="ly-files-actions">
        <span>已选中 {{ selected.length }} 项</span>
        <el-button link type="primary" :icon="'Download'" @click="downloadSelected">下载</el-button>
        <el-button link type="primary" :icon="'Rank'" @click="openMove('move')">移动</el-button>
        <el-button link type="primary" :icon="'CopyDocument'" @click="openMove('copy')">复制</el-button>
        <el-button link type="danger" :icon="'Delete'" @click="removeNodes(selected)">删除</el-button>
        <span class="ly-spacer"></span>
        <el-button link @click="selected = []">取消选择</el-button>
      </div>

      <el-table
        v-loading="loading"
        :data="items"
        row-key="id"
        class="ly-files-table"
        @selection-change="(rows: FileNode[]) => (selected = rows)"
        @row-dblclick="openNode"
      >
        <el-table-column type="selection" width="44" />
        <el-table-column label="名称" min-width="300">
          <template #default="{ row }">
            <div class="ly-file-row" @click="openNode(row)">
              <FileIcon :name="row.name" :is-dir="row.is_dir" />
              <div class="ly-file-meta">
                <span class="ly-file-name">{{ row.name }}</span>
                <span v-if="row.creator_name" class="ly-file-sub">{{ row.creator_name }}</span>
              </div>
              <span v-if="row.is_pdf" class="ly-tag">PDF</span>
              <span v-else-if="row.editable && store.officeEnabled" class="ly-tag ly-tag--primary">
                可在线编辑
              </span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="110">
          <template #default="{ row }">
            <span :class="{ 'ly-muted': row.is_dir }">{{ row.is_dir ? '—' : row.size_text }}</span>
          </template>
        </el-table-column>
        <el-table-column label="修改时间" width="140">
          <template #default="{ row }">
            <span class="ly-muted">{{ relativeTime(row.updated_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="180" align="right">
          <template #default="{ row }">
            <el-button
              v-if="row.perms.includes('download')"
              link
              type="primary"
              size="small"
              @click.stop="download(row)"
            >
              下载
            </el-button>
            <el-button
              v-if="row.perms.includes('share')"
              link
              type="primary"
              size="small"
              @click.stop="openShare(row)"
            >
              分享
            </el-button>
            <el-dropdown trigger="click" @command="(cmd: string) => onRowCommand(cmd, row)">
              <el-button link size="small" @click.stop>
                更多<el-icon><ArrowDown /></el-icon>
              </el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item v-if="row.is_pdf" command="preview">
                    <el-icon><View /></el-icon> 预览
                  </el-dropdown-item>
                  <el-dropdown-item
                    v-if="
                      row.perms.includes('edit') &&
                      (row.is_pdf ? store.pdfEditEnabled : row.editable && store.officeEnabled)
                    "
                    command="office"
                  >
                    <el-icon><EditPen /></el-icon> 在线编辑
                  </el-dropdown-item>
                  <el-dropdown-item v-if="row.perms.includes('edit')" command="rename">
                    <el-icon><Edit /></el-icon> 重命名
                  </el-dropdown-item>
                  <el-dropdown-item v-if="row.perms.includes('manage')" command="perm">
                    <el-icon><Key /></el-icon> 权限设置
                  </el-dropdown-item>
                  <el-dropdown-item v-if="row.perms.includes('delete')" command="delete" divided>
                    <el-icon><Delete /></el-icon> 删除
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">
            <el-icon :size="42" class="ly-muted"><FolderOpened /></el-icon>
            <p v-if="keyword">没有匹配「{{ keyword }}」的文件</p>
            <p v-else-if="canUpload">这里还是空的，拖拽文件到此处即可上传</p>
            <p v-else>这里还没有内容</p>
          </div>
        </template>
      </el-table>
    </div>

    <div v-if="dragOver" class="ly-drop-hint">
      <el-icon :size="44"><UploadFilled /></el-icon>
      <p>松开即可上传到当前目录</p>
    </div>

    <UploadPanel :tasks="tasks" @cancel="cancelTask" @clear="clearTasks" />

    <PermissionDialog
      v-model="permDialog"
      :space-id="spaceId"
      :node-id="permTarget.nodeId"
      :title="permTarget.title"
    />
    <ShareDialog v-model="shareDialog" :space-id="spaceId" :node="shareNode" />
    <MoveDialog
      v-model="moveDialog"
      :mode="moveMode"
      :space-id="spaceId"
      :node-ids="selectedIds"
      @done="load"
    />
    <PreviewDialog
      v-model="previewDialog"
      :space-id="spaceId"
      :node="previewNode"
      :can-download="previewCanDownload"
      :can-edit-pdf="previewCanEditPdf"
      @edit-pdf="
        (n) => {
          previewDialog = false
          openInOffice(n)
        }
      "
    />
  </div>
</template>

<style scoped>
.ly-files {
  position: relative;
  min-height: calc(100vh - var(--ly-header-height));
}

.ly-files-bar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--ly-border);
  flex-wrap: wrap;
}

.ly-files-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 8px 16px;
  background: var(--ly-primary-soft);
  border-bottom: 1px solid var(--ly-primary-border);
  font-size: 13px;
  color: var(--ly-primary);
}

.ly-files-table :deep(.el-table__row) {
  cursor: default;
}

.ly-file-row {
  display: flex;
  align-items: center;
  gap: 11px;
  cursor: pointer;
  min-width: 0;
}
.ly-file-meta {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.35;
}
.ly-file-name {
  font-size: 14px;
  color: var(--ly-text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-file-row:hover .ly-file-name {
  color: var(--ly-primary);
}
.ly-file-sub {
  font-size: 11.5px;
  color: var(--ly-text-tertiary);
}

.ly-drop-hint {
  position: absolute;
  inset: 12px;
  border: 2px dashed var(--ly-primary);
  border-radius: var(--ly-radius-lg);
  background: rgba(31, 94, 255, 0.05);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  color: var(--ly-primary);
  font-size: 15px;
  pointer-events: none;
  z-index: 10;
}
.ly-drop-hint p {
  margin: 0;
}
</style>
