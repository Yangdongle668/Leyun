<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api'
import type { Overview } from '@/api/types'
import { humanSize, spaceTypeLabel } from '@/utils/format'

const loading = ref(false)
const data = ref<Overview | null>(null)

async function load() {
  loading.value = true
  try {
    data.value = await api.adminOverview()
  } finally {
    loading.value = false
  }
}
onMounted(load)

const stats = computed(() => {
  const d = data.value
  if (!d) return []
  return [
    { label: '账号总数', value: d.user_total, sub: `${d.user_active} 个正常`, icon: 'UserFilled' },
    { label: '部门数', value: d.dept_total, sub: `${d.space_total} 个空间`, icon: 'Connection' },
    { label: '文件数', value: d.file_total, sub: `${d.folder_total} 个目录`, icon: 'Document' },
    { label: '近七日上传', value: d.recent_uploads, sub: `回收站 ${d.trash_total} 项`, icon: 'Upload' },
  ]
})

/** 去重率：省下来的空间占逻辑总量的比例。 */
const dedupPercent = computed(() => {
  const d = data.value
  if (!d || !d.logical_bytes) return 0
  return Math.round((d.dedup_saved / d.logical_bytes) * 100)
})

const maxDeptUsage = computed(() =>
  Math.max(1, ...(data.value?.dept_usage ?? []).map((d) => d.used)),
)
</script>

<template>
  <div class="ly-page" v-loading="loading">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">概览</h1>
        <p class="ly-page-desc">系统整体使用情况。</p>
      </div>
      <el-button :icon="'Refresh'" @click="load">刷新</el-button>
    </div>

    <!-- 指标卡 -->
    <div class="ly-stats">
      <div v-for="s in stats" :key="s.label" class="ly-card ly-stat">
        <div class="ly-stat-icon">
          <el-icon :size="20"><component :is="s.icon" /></el-icon>
        </div>
        <div>
          <div class="ly-stat-value">{{ s.value }}</div>
          <div class="ly-stat-label">{{ s.label }}</div>
          <div class="ly-stat-sub">{{ s.sub }}</div>
        </div>
      </div>
    </div>

    <div class="ly-grid">
      <!-- 存储 -->
      <section class="ly-card ly-card-pad">
        <h3 class="ly-section-title">存储用量</h3>
        <div v-if="data" class="ly-storage">
          <div class="ly-storage-main">
            <span class="ly-storage-num">{{ data.stored_text }}</span>
            <span class="ly-muted">实际占用磁盘</span>
          </div>
          <!-- 还没有任何文件时不画满条，否则 0 B 配一条实心蓝会让人误以为满了 -->
          <div class="ly-storage-bar">
            <div
              class="ly-storage-fill"
              :style="{ width: data.logical_bytes ? `${100 - dedupPercent}%` : '0%' }"
            ></div>
          </div>
          <ul class="ly-storage-legend">
            <li>
              <span class="dot dot-used"></span>
              实际占用 {{ data.stored_text }}
            </li>
            <li>
              <span class="dot dot-saved"></span>
              去重节省 {{ data.dedup_saved_text }}（{{ dedupPercent }}%）
            </li>
            <li class="ly-muted">逻辑总量 {{ data.logical_text }}</li>
          </ul>
          <p class="ly-storage-note">
            相同内容的文件在磁盘上只保存一份，多个目录引用同一份内容不会重复占用空间。
          </p>
        </div>
      </section>

      <!-- 空间排行 -->
      <section class="ly-card ly-card-pad">
        <h3 class="ly-section-title">空间用量 Top</h3>
        <el-table :data="data?.top_spaces ?? []" size="small">
          <el-table-column label="空间" min-width="160">
            <template #default="{ row }">
              <span>{{ row.name }}</span>
              <span class="ly-tag" style="margin-left: 6px">{{ spaceTypeLabel(row.type) }}</span>
            </template>
          </el-table-column>
          <el-table-column label="已用" width="100" prop="used_text" />
          <el-table-column label="配额" width="100">
            <template #default="{ row }">
              <span class="ly-muted">{{ row.quota ? humanSize(row.quota) : '不限' }}</span>
            </template>
          </el-table-column>
        </el-table>
      </section>
    </div>

    <!-- 部门用量 -->
    <section class="ly-card ly-card-pad" style="margin-top: 18px">
      <h3 class="ly-section-title">部门用量</h3>
      <div v-if="!data?.dept_usage?.length" class="ly-empty">暂无部门数据</div>
      <div v-else class="ly-dept-usage">
        <div v-for="d in data.dept_usage" :key="d.dept_id" class="ly-dept-row">
          <span class="ly-dept-name">{{ d.name }}</span>
          <span class="ly-dept-members">{{ d.members }} 人</span>
          <div class="ly-dept-bar">
            <div class="ly-dept-fill" :style="{ width: `${(d.used / maxDeptUsage) * 100}%` }"></div>
          </div>
          <span class="ly-dept-size">{{ d.used_text }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.ly-stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(212px, 1fr));
  gap: 16px;
  margin-bottom: 18px;
}
.ly-stat {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 18px 20px;
}
.ly-stat-icon {
  width: 42px;
  height: 42px;
  border-radius: 12px;
  background: var(--ly-primary-soft);
  color: var(--ly-primary);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.ly-stat-value {
  font-size: 24px;
  font-weight: 600;
  line-height: 1.2;
}
.ly-stat-label {
  font-size: 13px;
  color: var(--ly-text-secondary);
}
.ly-stat-sub {
  font-size: 11.5px;
  color: var(--ly-text-tertiary);
}

.ly-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
  gap: 18px;
  align-items: start;
}

.ly-section-title {
  margin: 0 0 16px;
  font-size: 15px;
  font-weight: 600;
}

.ly-storage-main {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 12px;
}
.ly-storage-num {
  font-size: 28px;
  font-weight: 600;
}
.ly-storage-bar {
  height: 8px;
  border-radius: 8px;
  background: #c8dbff;
  overflow: hidden;
  margin-bottom: 14px;
}
.ly-storage-fill {
  height: 100%;
  background: var(--ly-primary);
  border-radius: 8px;
}
.ly-storage-legend {
  list-style: none;
  margin: 0 0 12px;
  padding: 0;
  font-size: 13px;
  line-height: 2;
}
.ly-storage-legend .dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 3px;
  margin-right: 8px;
}
.dot-used {
  background: var(--ly-primary);
}
.dot-saved {
  background: #c8dbff;
}
.ly-storage-note {
  margin: 0;
  font-size: 12px;
  color: var(--ly-text-tertiary);
  line-height: 1.8;
}

.ly-dept-usage {
  display: grid;
  gap: 12px;
}
.ly-dept-row {
  display: grid;
  grid-template-columns: 160px 60px 1fr 90px;
  align-items: center;
  gap: 12px;
  font-size: 13px;
}
.ly-dept-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-dept-members {
  color: var(--ly-text-tertiary);
  font-size: 12px;
}
.ly-dept-bar {
  height: 6px;
  border-radius: 6px;
  background: var(--ly-surface-sunken);
  overflow: hidden;
}
.ly-dept-fill {
  height: 100%;
  background: var(--ly-primary);
  border-radius: 6px;
  min-width: 2px;
}
.ly-dept-size {
  text-align: right;
  color: var(--ly-text-secondary);
}

@media (max-width: 720px) {
  .ly-dept-row {
    grid-template-columns: 1fr 70px;
  }
  .ly-dept-bar {
    display: none;
  }
}
</style>
