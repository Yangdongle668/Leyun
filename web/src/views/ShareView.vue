<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { api, ApiError } from '@/api'
import type { FileNode } from '@/api/types'
import { extOf, humanSize, relativeTime } from '@/utils/format'
import FileIcon from '@/components/FileIcon.vue'
import PreviewDialog from '@/components/PreviewDialog.vue'
import PdfViewer from '@/components/PdfViewer.vue'

const route = useRoute()
const code = String(route.params.code)

const password = ref('')
const needPassword = ref(false)
const loading = ref(true)
const error = ref('')
const info = ref<Awaited<ReturnType<typeof api.shareInfo>> | null>(null)
const items = ref<FileNode[]>([])
const crumbs = ref<Array<{ id: number; name: string }>>([])
const parentId = ref(0)
const previewDialog = ref(false)
const previewNode = ref<FileNode | null>(null)

async function open() {
  loading.value = true
  error.value = ''
  try {
    info.value = await api.shareInfo(code, password.value || undefined)
    needPassword.value = false
    if (info.value.node.is_dir) {
      await listDir(0)
    }
  } catch (err) {
    if (err instanceof ApiError && err.code === 42901) {
      needPassword.value = true
    } else {
      error.value = err instanceof Error ? err.message : '无法打开分享'
    }
  } finally {
    loading.value = false
  }
}

async function listDir(pid: number) {
  const res = await api.shareList(code, { password: password.value || undefined, parent_id: pid })
  items.value = res.items
  crumbs.value = res.crumbs
  parentId.value = pid
}

onMounted(open)

function submitPassword() {
  if (!password.value.trim()) {
    ElMessage.warning('请输入提取码')
    return
  }
  open()
}

function downloadURL(nodeId?: number) {
  const url = new URL(`/api/v1/share/${code}/download`, window.location.origin)
  if (nodeId) url.searchParams.set('node_id', String(nodeId))
  if (password.value) url.searchParams.set('password', password.value)
  return url.toString()
}

/** 模板里拿不到 window，下载统一走这个方法。 */
function goDownload(nodeId?: number) {
  window.location.href = downloadURL(nodeId)
}

const canDownload = () => !!info.value?.share.perms.includes('download')

/** 分享的是单个 PDF 时，直接在页面里铺开看，不用再点一次。 */
const isPdf = computed(
  () => !!info.value && !info.value.node.is_dir && extOf(info.value.node.name) === 'pdf',
)

const previewSrc = computed(() => {
  const url = new URL(`/api/v1/share/${code}/preview`, window.location.origin)
  if (password.value) url.searchParams.set('password', password.value)
  return url.toString()
})

function openNode(node: FileNode) {
  if (node.is_dir) {
    listDir(node.id)
    return
  }
  if (node.previewable) {
    previewNode.value = node
    previewDialog.value = true
    return
  }
  if (canDownload()) window.location.href = downloadURL(node.id)
}
</script>

<template>
  <div class="ly-share-page">
    <header class="ly-share-head">
      <div class="ly-share-logo">
        <span class="mark">乐</span>
        <span>乐云企业网盘</span>
      </div>
    </header>

    <main class="ly-share-main">
      <!-- 需要提取码 -->
      <div v-if="needPassword" class="ly-share-card ly-share-gate">
        <el-icon :size="38" color="#1f5eff"><Lock /></el-icon>
        <h2>请输入提取码</h2>
        <p class="ly-muted">该分享设置了提取码，向分享者索取后即可查看。</p>
        <div class="ly-share-gate-form">
          <el-input
            v-model="password"
            placeholder="提取码"
            size="large"
            maxlength="16"
            @keyup.enter="submitPassword"
          />
          <el-button type="primary" size="large" @click="submitPassword">确定</el-button>
        </div>
      </div>

      <!-- 打不开 -->
      <div v-else-if="error" class="ly-share-card ly-share-gate">
        <el-icon :size="38" class="ly-muted"><WarningFilled /></el-icon>
        <h2>无法打开该分享</h2>
        <p class="ly-muted">{{ error }}</p>
      </div>

      <!-- 正常内容 -->
      <div v-else-if="info" v-loading="loading" class="ly-share-card">
        <div class="ly-share-title">
          <FileIcon :name="info.node.name" :is-dir="info.node.is_dir" :size="44" />
          <div class="ly-share-title-meta">
            <h2>{{ info.node.name }}</h2>
            <p class="ly-muted">
              来自「{{ info.space_name }}」
              <template v-if="!info.node.is_dir"> · {{ info.node.size_text }}</template>
              <template v-if="info.share.expire_at">
                · 有效期至 {{ relativeTime(info.share.expire_at) }}
              </template>
            </p>
          </div>
          <span class="ly-spacer"></span>
          <el-button
            v-if="canDownload() && !info.node.is_dir"
            type="primary"
            :icon="'Download'"
            @click="goDownload()"
          >
            下载
          </el-button>
        </div>

        <template v-if="info.node.is_dir">
          <el-breadcrumb separator="/" class="ly-share-crumbs">
            <el-breadcrumb-item v-for="c in crumbs" :key="c.id">
              <a href="javascript:void(0)" @click="listDir(c.id)">{{ c.name }}</a>
            </el-breadcrumb-item>
          </el-breadcrumb>

          <el-table :data="items" row-key="id">
            <el-table-column label="名称" min-width="280">
              <template #default="{ row }">
                <div class="ly-share-row" @click="openNode(row)">
                  <FileIcon :name="row.name" :is-dir="row.is_dir" :size="30" />
                  <span>{{ row.name }}</span>
                </div>
              </template>
            </el-table-column>
            <el-table-column label="大小" width="120">
              <template #default="{ row }">
                <span :class="{ 'ly-muted': row.is_dir }">{{ row.is_dir ? '—' : humanSize(row.size) }}</span>
              </template>
            </el-table-column>
            <el-table-column label="修改时间" width="140">
              <template #default="{ row }">
                <span class="ly-muted">{{ relativeTime(row.updated_at) }}</span>
              </template>
            </el-table-column>
            <el-table-column width="100" align="right">
              <template #default="{ row }">
                <el-button
                  v-if="canDownload() && !row.is_dir"
                  link
                  type="primary"
                  size="small"
                  @click.stop="goDownload(row.id)"
                >
                  下载
                </el-button>
              </template>
            </el-table-column>
            <template #empty>
              <div class="ly-empty">这个目录是空的</div>
            </template>
          </el-table>
        </template>

        <!-- 单文件分享：PDF 直接铺开看，不用再点一次 -->
        <div v-else-if="isPdf" class="ly-share-pdf">
          <PdfViewer :src="previewSrc" :file-name="info.node.name" :can-download="canDownload()" />
        </div>

        <div v-else-if="!canDownload()" class="ly-share-note">
          分享者仅开放了在线查看，未授予下载权限。
        </div>
      </div>
    </main>

    <!-- 分享里的 PDF 同样受分享权限管控：只给查看时，阅读器不提供下载与打印 -->
    <PreviewDialog
      v-model="previewDialog"
      :space-id="0"
      :node="previewNode"
      :share-code="code"
      :share-password="password"
      :can-download="canDownload()"
    />
  </div>
</template>

<style scoped>
.ly-share-page {
  min-height: 100vh;
  background: var(--ly-bg);
}
.ly-share-head {
  height: 60px;
  display: flex;
  align-items: center;
  padding: 0 28px;
  background: var(--ly-surface);
  border-bottom: 1px solid var(--ly-border);
}
.ly-share-logo {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 15px;
  font-weight: 600;
}
.ly-share-logo .mark {
  width: 28px;
  height: 28px;
  border-radius: 8px;
  background: var(--ly-primary);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
}

.ly-share-main {
  max-width: 960px;
  margin: 0 auto;
  padding: 36px 20px 60px;
}
.ly-share-card {
  background: var(--ly-surface);
  border: 1px solid var(--ly-border);
  border-radius: var(--ly-radius-lg);
  box-shadow: var(--ly-shadow-sm);
  overflow: hidden;
}

.ly-share-gate {
  padding: 56px 32px;
  text-align: center;
}
.ly-share-gate h2 {
  margin: 16px 0 6px;
  font-size: 19px;
  font-weight: 600;
}
.ly-share-gate p {
  margin: 0 0 24px;
  font-size: 13px;
}
.ly-share-gate-form {
  display: flex;
  gap: 10px;
  max-width: 320px;
  margin: 0 auto;
}

.ly-share-title {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 24px 26px;
  border-bottom: 1px solid var(--ly-border);
}
.ly-share-title-meta {
  min-width: 0;
}
.ly-share-title h2 {
  margin: 0 0 4px;
  font-size: 17px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-share-title p {
  margin: 0;
  font-size: 13px;
}

.ly-share-crumbs {
  padding: 14px 26px 6px;
}
.ly-share-row {
  display: flex;
  align-items: center;
  gap: 10px;
  cursor: pointer;
  min-width: 0;
}
.ly-share-row > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-share-row:hover > span {
  color: var(--ly-primary);
}

.ly-share-pdf {
  height: 74vh;
  min-height: 420px;
}

.ly-share-note {
  padding: 36px 26px;
  text-align: center;
  color: var(--ly-text-tertiary);
  font-size: 13px;
}
</style>
