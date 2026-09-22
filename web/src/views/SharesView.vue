<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '@/api'
import type { Share } from '@/api/types'
import { formatTime, permLabels2Text } from '@/utils/format'

const loading = ref(false)
const list = ref<Share[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

async function load() {
  loading.value = true
  try {
    const res = await api.listShares({ page: page.value, page_size: pageSize.value, mine: true })
    list.value = res.list
    total.value = res.total
  } finally {
    loading.value = false
  }
}

onMounted(load)

function linkOf(share: Share) {
  return `${window.location.origin}/s/${share.code}`
}

async function copy(share: Share) {
  try {
    await navigator.clipboard.writeText(linkOf(share))
    ElMessage.success('链接已复制')
  } catch {
    ElMessage.info('当前环境不支持自动复制，请手动选中链接')
  }
}

async function revoke(share: Share) {
  await ElMessageBox.confirm(
    `撤销后该链接立即失效，已经拿到链接的人将无法再访问「${share.node_name}」。确定撤销吗？`,
    '撤销分享',
    { type: 'warning', confirmButtonText: '撤销', cancelButtonText: '取消' },
  )
  await api.revokeShare(share.id)
  ElMessage.success('已撤销')
  await load()
}

function statusOf(share: Share) {
  if (share.revoked) return { text: '已撤销', cls: 'ly-tag' }
  if (share.expired) return { text: '已过期', cls: 'ly-tag ly-tag--warning' }
  return { text: '生效中', cls: 'ly-tag ly-tag--success' }
}
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">我的分享</h1>
        <p class="ly-page-desc">这里列出你创建的全部分享链接，可随时撤销。</p>
      </div>
    </div>

    <div class="ly-card">
      <el-table v-loading="loading" :data="list" row-key="id">
        <el-table-column label="内容" min-width="220">
          <template #default="{ row }">
            <div class="ly-share-name">
              <el-icon v-if="row.is_dir" color="#f5a524"><Folder /></el-icon>
              <el-icon v-else class="ly-muted"><Document /></el-icon>
              <span>{{ row.node_name }}</span>
            </div>
            <span class="ly-muted" style="font-size: 12px">{{ row.space_name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="链接" min-width="240">
          <template #default="{ row }">
            <span class="ly-mono">{{ linkOf(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="范围" width="110">
          <template #default="{ row }">
            <span class="ly-tag" :class="row.scope === 'public' ? 'ly-tag--warning' : 'ly-tag--primary'">
              {{ row.scope === 'public' ? '对外公开' : '企业内部' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="访客权限" width="130">
          <template #default="{ row }">
            <span class="ly-muted">{{ permLabels2Text(row.perm_codes) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="提取码" width="80" align="center">
          <template #default="{ row }">
            <el-icon v-if="row.has_password" color="#12b76a"><Lock /></el-icon>
            <span v-else class="ly-muted">无</span>
          </template>
        </el-table-column>
        <el-table-column label="访问 / 下载" width="110" align="center">
          <template #default="{ row }">
            <span class="ly-muted">{{ row.views }} / {{ row.downloads }}</span>
          </template>
        </el-table-column>
        <el-table-column label="有效期" width="160">
          <template #default="{ row }">
            <span class="ly-muted">{{ row.expire_at ? formatTime(row.expire_at, false) : '长期' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <span :class="statusOf(row).cls">{{ statusOf(row).text }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="130" align="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="copy(row)">复制</el-button>
            <el-button v-if="!row.revoked" link type="danger" size="small" @click="revoke(row)">
              撤销
            </el-button>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">
            <el-icon :size="42" class="ly-muted"><Link /></el-icon>
            <p>还没有创建过分享</p>
          </div>
        </template>
      </el-table>

      <div v-if="total > pageSize" class="ly-pager">
        <el-pagination
          v-model:current-page="page"
          :page-size="pageSize"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="load"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.ly-share-name {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}
.ly-share-name > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-pager {
  display: flex;
  justify-content: flex-end;
  padding: 14px 16px;
  border-top: 1px solid var(--ly-border);
}
</style>
