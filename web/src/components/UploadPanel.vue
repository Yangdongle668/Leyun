<script setup lang="ts">
import { computed, ref } from 'vue'
import { humanSize } from '@/utils/format'
import type { UploadTask } from '@/utils/upload'

const props = defineProps<{ tasks: UploadTask[] }>()
const emit = defineEmits<{ cancel: [UploadTask]; clear: [] }>()

const collapsed = ref(false)

/** 还没跑完的：排队中的也算，不然整批传到一半会显示"已完成"。 */
const active = computed(() =>
  props.tasks.filter(
    (t) => t.status === 'hashing' || t.status === 'uploading' || t.status === 'queued',
  ),
)
const waiting = computed(() => props.tasks.filter((t) => t.status === 'queued').length)
const failed = computed(() => props.tasks.filter((t) => t.status === 'error'))
const doneCount = computed(() => props.tasks.filter((t) => t.status === 'done').length)

const summary = computed(() => {
  if (active.value.length) {
    const tail = waiting.value ? `，${waiting.value} 个排队中` : ''
    return `正在上传 ${active.value.length - waiting.value} 个文件${tail}`
  }
  if (failed.value.length) return `${failed.value.length} 个文件上传失败`
  return `已完成 ${doneCount.value} 个文件`
})

function statusText(t: UploadTask) {
  switch (t.status) {
    case 'queued':
      return '排队中…'
    case 'hashing':
      return '计算校验值…'
    case 'uploading':
      return `${t.progress}%`
    case 'done':
      return t.instant ? '秒传完成' : '已完成'
    case 'error':
      return t.message || '失败'
    case 'canceled':
      return '已取消'
  }
}
</script>

<template>
  <div v-if="tasks.length" class="ly-upload" :class="{ 'is-collapsed': collapsed }">
    <header class="ly-upload-head" @click="collapsed = !collapsed">
      <el-icon v-if="active.length" class="is-spin"><Loading /></el-icon>
      <el-icon v-else-if="failed.length" color="#f04438"><CircleCloseFilled /></el-icon>
      <el-icon v-else color="#12b76a"><CircleCheckFilled /></el-icon>
      <span class="ly-upload-title">{{ summary }}</span>
      <span class="ly-spacer"></span>
      <button
        v-if="!active.length"
        class="ly-upload-btn"
        title="清空列表"
        @click.stop="emit('clear')"
      >
        <el-icon><Delete /></el-icon>
      </button>
      <button class="ly-upload-btn" :title="collapsed ? '展开' : '收起'">
        <el-icon><ArrowUp v-if="collapsed" /><ArrowDown v-else /></el-icon>
      </button>
    </header>

    <div v-show="!collapsed" class="ly-upload-list">
      <div v-for="t in tasks" :key="t.id" class="ly-upload-item">
        <div class="ly-upload-info">
          <span class="ly-upload-name" :title="t.name">{{ t.name }}</span>
          <span class="ly-upload-size">{{ humanSize(t.size) }}</span>
        </div>
        <div class="ly-upload-bar">
          <div
            class="ly-upload-fill"
            :class="{
              'is-done': t.status === 'done',
              'is-error': t.status === 'error' || t.status === 'canceled',
            }"
            :style="{ width: `${t.status === 'done' ? 100 : t.progress}%` }"
          ></div>
        </div>
        <div class="ly-upload-status">
          <span :class="{ 'is-error': t.status === 'error' }">{{ statusText(t) }}</span>
          <button
            v-if="t.status === 'uploading' || t.status === 'hashing' || t.status === 'queued'"
            class="ly-upload-btn"
            title="取消"
            @click="emit('cancel', t)"
          >
            <el-icon><Close /></el-icon>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ly-upload {
  position: fixed;
  right: 24px;
  bottom: 24px;
  width: 400px;
  max-width: calc(100vw - 32px);
  background: var(--ly-surface);
  border: 1px solid var(--ly-border);
  border-radius: var(--ly-radius);
  box-shadow: var(--ly-shadow-lg);
  z-index: 2000;
  overflow: hidden;
}

.ly-upload-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--ly-border);
  cursor: pointer;
  user-select: none;
}
.is-collapsed .ly-upload-head {
  border-bottom: none;
}
.ly-upload-title {
  font-size: 13px;
  font-weight: 500;
}
.ly-upload-btn {
  width: 24px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--ly-text-tertiary);
  cursor: pointer;
}
.ly-upload-btn:hover {
  background: var(--ly-surface-sunken);
  color: var(--ly-text);
}

.ly-upload-list {
  max-height: 280px;
  overflow-y: auto;
  padding: 6px 0;
}
.ly-upload-item {
  padding: 8px 14px;
}
.ly-upload-info {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 6px;
}
.ly-upload-name {
  flex: 1;
  min-width: 0;
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-upload-size {
  font-size: 11px;
  color: var(--ly-text-tertiary);
  flex-shrink: 0;
}
.ly-upload-bar {
  height: 3px;
  border-radius: 3px;
  background: var(--ly-border);
  overflow: hidden;
}
.ly-upload-fill {
  height: 100%;
  background: var(--ly-primary);
  border-radius: 3px;
  transition: width 0.25s ease;
}
.ly-upload-fill.is-done {
  background: var(--ly-success);
}
.ly-upload-fill.is-error {
  background: var(--ly-danger);
}
.ly-upload-status {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 4px;
  margin-top: 4px;
  font-size: 11px;
  color: var(--ly-text-tertiary);
}
.ly-upload-status .is-error {
  color: var(--ly-danger);
}

.is-spin {
  animation: ly-spin 1s linear infinite;
}
@keyframes ly-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
