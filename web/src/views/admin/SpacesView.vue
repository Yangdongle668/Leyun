<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '@/api'
import type { Space } from '@/api/types'
import { useUserStore } from '@/stores/user'
import { bytesToGB, gbToBytes, humanSize, spaceTypeLabel } from '@/utils/format'
import PermissionDialog from '@/components/PermissionDialog.vue'

const store = useUserStore()
const loading = ref(false)
const list = ref<Space[]>([])

const dialog = reactive({ visible: false, id: 0, name: '', quotaGB: 0, submitting: false })
const permDialog = ref(false)
const permTarget = reactive({ spaceId: 0, title: '' })

async function load() {
  loading.value = true
  try {
    list.value = await api.adminSpaces()
  } finally {
    loading.value = false
  }
}
onMounted(load)

function usedPercent(sp: Space) {
  if (!sp.quota_bytes) return 0
  return Math.min(100, Math.round((sp.used_bytes / sp.quota_bytes) * 100))
}

function openEdit(sp: Space) {
  dialog.id = sp.id
  dialog.name = sp.name
  dialog.quotaGB = bytesToGB(sp.quota_bytes)
  dialog.visible = true
}

async function submit() {
  dialog.submitting = true
  try {
    await api.updateSpace(dialog.id, { name: dialog.name.trim(), quota: gbToBytes(dialog.quotaGB) })
    ElMessage.success('空间已更新')
    dialog.visible = false
    await Promise.all([load(), store.refreshSpaces()])
  } finally {
    dialog.submitting = false
  }
}

function openPerm(sp: Space) {
  permTarget.spaceId = sp.id
  permTarget.title = sp.name
  permDialog.value = true
}
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">空间管理</h1>
        <p class="ly-page-desc">调整各空间的名称与配额，或直接在这里设置空间级权限。</p>
      </div>
    </div>

    <div class="ly-card">
      <el-table v-loading="loading" :data="list" row-key="id">
        <el-table-column label="空间" min-width="220">
          <template #default="{ row }">
            <div class="ly-space-cell">
              <el-icon :size="18" :color="row.type === 'public' ? '#12b76a' : row.type === 'department' ? '#1f5eff' : '#98a1b3'">
                <Share v-if="row.type === 'public'" />
                <OfficeBuilding v-else-if="row.type === 'department'" />
                <User v-else />
              </el-icon>
              <div>
                <div>{{ row.name }}</div>
                <div class="ly-muted" style="font-size: 12px">
                  {{ spaceTypeLabel(row.type) }}
                  <template v-if="row.owner_name"> · {{ row.owner_name }}</template>
                </div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="用量" min-width="220">
          <template #default="{ row }">
            <div class="ly-space-usage">
              <span>
                {{ humanSize(row.used_bytes) }}
                <span class="ly-muted">/ {{ row.quota_bytes ? humanSize(row.quota_bytes) : '不限' }}</span>
              </span>
              <div v-if="row.quota_bytes" class="ly-space-bar">
                <div
                  class="ly-space-fill"
                  :class="{ 'is-warn': usedPercent(row) >= 85 }"
                  :style="{ width: `${usedPercent(row)}%` }"
                ></div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <span class="ly-tag" :class="row.enabled ? 'ly-tag--success' : 'ly-tag--danger'">
              {{ row.enabled ? '正常' : '已停用' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="220" align="right">
          <template #default="{ row }">
            <router-link :to="{ name: 'files', params: { spaceId: String(row.id) } }">
              <el-button link type="primary" size="small">进入</el-button>
            </router-link>
            <el-button link type="primary" size="small" @click="openPerm(row)">权限</el-button>
            <el-button v-if="store.isSuperAdmin" link type="primary" size="small" @click="openEdit(row)">
              编辑
            </el-button>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">还没有空间</div>
        </template>
      </el-table>
    </div>

    <el-dialog v-model="dialog.visible" title="编辑空间" width="440px" destroy-on-close>
      <el-form label-width="80px" label-position="left">
        <el-form-item label="名称">
          <el-input v-model="dialog.name" maxlength="64" />
        </el-form-item>
        <el-form-item label="配额">
          <el-input-number v-model="dialog.quotaGB" :min="0" :max="1024000" style="width: 160px" />
          <span class="ly-muted" style="margin-left: 10px">GB，0 表示不限</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.submitting" @click="submit">保存</el-button>
      </template>
    </el-dialog>

    <PermissionDialog
      v-model="permDialog"
      :space-id="permTarget.spaceId"
      :node-id="0"
      :title="permTarget.title"
    />
  </div>
</template>

<style scoped>
.ly-space-cell {
  display: flex;
  align-items: center;
  gap: 10px;
}
.ly-space-usage {
  display: flex;
  flex-direction: column;
  gap: 5px;
  font-size: 13px;
}
.ly-space-bar {
  height: 4px;
  width: 160px;
  border-radius: 4px;
  background: var(--ly-surface-sunken);
  overflow: hidden;
}
.ly-space-fill {
  height: 100%;
  background: var(--ly-primary);
  border-radius: 4px;
}
.ly-space-fill.is-warn {
  background: var(--ly-warning);
}
</style>
