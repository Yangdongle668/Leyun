<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { api } from '@/api'
import type { AuditLog } from '@/api/types'
import { actionLabel, formatTime } from '@/utils/format'

const loading = ref(false)
const list = ref<AuditLog[]>([])
const total = ref(0)
const actions = ref<string[]>([])

const query = reactive({
  username: '',
  action: '',
  keyword: '',
  success: '',
  page: 1,
  page_size: 20,
})

async function load() {
  loading.value = true
  try {
    const res = await api.auditLogs({
      username: query.username || undefined,
      action: query.action || undefined,
      keyword: query.keyword || undefined,
      success: query.success || undefined,
      page: query.page,
      page_size: query.page_size,
    })
    list.value = res.list
    total.value = res.total
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await Promise.all([load(), api.auditActions().then((a) => (actions.value = a))])
})

function search() {
  query.page = 1
  load()
}

function reset() {
  Object.assign(query, { username: '', action: '', keyword: '', success: '', page: 1 })
  load()
}
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">审计日志</h1>
        <p class="ly-page-desc">记录谁、什么时候、对哪个文件做了什么，便于事后追溯。</p>
      </div>
    </div>

    <div class="ly-card">
      <div class="ly-filters">
        <el-input
          v-model="query.username"
          placeholder="操作人"
          :prefix-icon="'User'"
          clearable
          style="width: 160px"
          @keyup.enter="search"
        />
        <el-select v-model="query.action" placeholder="全部动作" clearable style="width: 180px" @change="search">
          <el-option v-for="a in actions" :key="a" :label="actionLabel(a)" :value="a" />
        </el-select>
        <el-select v-model="query.success" placeholder="全部结果" clearable style="width: 130px" @change="search">
          <el-option value="true" label="成功" />
          <el-option value="false" label="失败" />
        </el-select>
        <el-input
          v-model="query.keyword"
          placeholder="搜索对象或详情"
          :prefix-icon="'Search'"
          clearable
          style="width: 220px"
          @keyup.enter="search"
        />
        <el-button type="primary" @click="search">查询</el-button>
        <el-button @click="reset">重置</el-button>
      </div>

      <el-table v-loading="loading" :data="list" row-key="id">
        <el-table-column label="时间" width="170">
          <template #default="{ row }">
            <span class="ly-muted ly-num">{{ formatTime(row.created_at) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作人" width="130">
          <template #default="{ row }">
            <span>{{ row.username || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="动作" width="130">
          <template #default="{ row }">
            <span class="ly-tag" :class="row.success ? '' : 'ly-tag--danger'">
              {{ actionLabel(row.action) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="对象" min-width="200">
          <template #default="{ row }">
            <span>{{ row.target || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="详情" min-width="200">
          <template #default="{ row }">
            <span class="ly-muted">{{ row.detail || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="IP" width="140">
          <template #default="{ row }">
            <span class="ly-muted ly-mono">{{ row.ip }}</span>
          </template>
        </el-table-column>
        <el-table-column label="结果" width="80">
          <template #default="{ row }">
            <span class="ly-tag" :class="row.success ? 'ly-tag--success' : 'ly-tag--danger'">
              {{ row.success ? '成功' : '失败' }}
            </span>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">没有符合条件的日志</div>
        </template>
      </el-table>

      <div v-if="total > query.page_size" class="ly-pager">
        <el-pagination
          v-model:current-page="query.page"
          :page-size="query.page_size"
          :total="total"
          layout="total, prev, pager, next"
          @current-change="load"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.ly-filters {
  display: flex;
  gap: 10px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--ly-border);
  flex-wrap: wrap;
}
.ly-pager {
  display: flex;
  justify-content: flex-end;
  padding: 14px 16px;
  border-top: 1px solid var(--ly-border);
}
</style>
