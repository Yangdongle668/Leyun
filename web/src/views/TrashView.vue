<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '@/api'
import type { TrashItem } from '@/api/types'
import { useUserStore } from '@/stores/user'
import { formatTime } from '@/utils/format'
import FileIcon from '@/components/FileIcon.vue'

const store = useUserStore()
const loading = ref(false)
const list = ref<TrashItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const selected = ref<TrashItem[]>([])

async function load() {
  loading.value = true
  try {
    const res = await api.listTrash({ page: page.value, page_size: pageSize.value })
    list.value = res.list
    total.value = res.total
    selected.value = []
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function restore(items: TrashItem[]) {
  if (!items.length) return
  const res = await api.restoreTrash(items.map((i) => i.id))
  ElMessage.success(`已还原 ${res.restored} 个条目`)
  await Promise.all([load(), store.refreshSpaces()])
}

async function purge(items: TrashItem[]) {
  const label = items.length === 1 ? `「${items[0].name}」` : `这 ${items.length} 个条目`
  await ElMessageBox.confirm(
    `将永久删除${label}及其全部内容，该操作无法撤销。确定继续吗？`,
    '彻底删除',
    { type: 'warning', confirmButtonText: '永久删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' },
  )
  const res = await api.purgeTrash(items.map((i) => i.id))
  ElMessage.success(`已彻底删除 ${res.purged} 个条目`)
  await Promise.all([load(), store.refreshSpaces()])
}

async function purgeAll() {
  await ElMessageBox.confirm(
    '将永久删除回收站中你有权处理的全部条目，该操作无法撤销。确定继续吗？',
    '清空回收站',
    { type: 'warning', confirmButtonText: '清空', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' },
  )
  const res = await api.purgeTrash([])
  ElMessage.success(`已清空 ${res.purged} 个条目`)
  await Promise.all([load(), store.refreshSpaces()])
}
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">回收站</h1>
        <p class="ly-page-desc">删除的文件会先进入这里，还原后回到原来的位置。</p>
      </div>
      <div class="ly-toolbar">
        <el-button
          v-if="selected.length"
          type="primary"
          :icon="'RefreshLeft'"
          @click="restore(selected)"
        >
          还原选中（{{ selected.length }}）
        </el-button>
        <el-button v-if="selected.length" type="danger" plain :icon="'Delete'" @click="purge(selected)">
          彻底删除
        </el-button>
        <el-button v-if="total" plain @click="purgeAll">清空回收站</el-button>
      </div>
    </div>

    <div class="ly-card">
      <el-table
        v-loading="loading"
        :data="list"
        row-key="id"
        @selection-change="(rows: TrashItem[]) => (selected = rows)"
      >
        <el-table-column type="selection" width="44" />
        <el-table-column label="名称" min-width="280">
          <template #default="{ row }">
            <div class="ly-trash-row">
              <FileIcon :name="row.name" :is-dir="row.is_dir" :size="30" />
              <span>{{ row.name }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="所属空间" width="160" prop="space_name" />
        <el-table-column label="大小" width="110">
          <template #default="{ row }">
            <span :class="{ 'ly-muted': row.is_dir }">{{ row.is_dir ? '目录' : row.size_text }}</span>
          </template>
        </el-table-column>
        <el-table-column label="删除时间" width="170">
          <template #default="{ row }">
            <span class="ly-muted ly-num">{{ formatTime(row.trashed_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" align="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="restore([row])">还原</el-button>
            <el-button link type="danger" size="small" @click="purge([row])">彻底删除</el-button>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">
            <el-icon :size="42" class="ly-muted"><Delete /></el-icon>
            <p>回收站是空的</p>
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
.ly-trash-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.ly-trash-row > span {
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
