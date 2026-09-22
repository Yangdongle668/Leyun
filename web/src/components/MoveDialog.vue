<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '@/api'
import type { FileNode, Space } from '@/api/types'
import { useUserStore } from '@/stores/user'

const props = defineProps<{
  modelValue: boolean
  mode: 'move' | 'copy'
  spaceId: number
  nodeIds: number[]
}>()
const emit = defineEmits<{ 'update:modelValue': [boolean]; done: [] }>()

const store = useUserStore()
const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const targetSpaceId = ref(0)
const currentParent = ref(0)
const crumbs = ref<Array<{ id: number; name: string }>>([])
const folders = ref<FileNode[]>([])
const loading = ref(false)
const submitting = ref(false)

/** 只能移动/复制到自己有上传权的空间。 */
const writableSpaces = computed<Space[]>(() =>
  store.spaces.filter((s) => s.perms.includes('upload')),
)

const actionLabel = computed(() => (props.mode === 'move' ? '移动' : '复制'))

watch(visible, async (open) => {
  if (!open) return
  targetSpaceId.value = props.spaceId
  currentParent.value = 0
  await loadFolders()
})

watch(targetSpaceId, async () => {
  currentParent.value = 0
  await loadFolders()
})

async function loadFolders() {
  if (!targetSpaceId.value) return
  loading.value = true
  try {
    const res = await api.listFiles({ space_id: targetSpaceId.value, parent_id: currentParent.value })
    // 目标只能是目录；同时排除正在被移动的目录自身，免得用户选了再被后端拒绝。
    folders.value = res.items.filter((n) => n.is_dir && !props.nodeIds.includes(n.id))
    crumbs.value = res.crumbs
  } finally {
    loading.value = false
  }
}

async function enter(node: FileNode) {
  currentParent.value = node.id
  await loadFolders()
}

async function jump(id: number) {
  currentParent.value = id
  await loadFolders()
}

async function submit() {
  if (!targetSpaceId.value) {
    ElMessage.warning('请选择目标空间')
    return
  }
  submitting.value = true
  try {
    const payload = {
      space_id: props.spaceId,
      node_ids: props.nodeIds,
      target_space_id: targetSpaceId.value,
      target_id: currentParent.value,
    }
    if (props.mode === 'move') {
      const res = await api.move(payload)
      ElMessage.success(`已移动 ${res.moved} 个条目`)
    } else {
      const res = await api.copy(payload)
      ElMessage.success(`已复制 ${res.copied} 个条目`)
    }
    visible.value = false
    emit('done')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog v-model="visible" :title="`${actionLabel}到…`" width="560px" destroy-on-close>
    <el-select v-model="targetSpaceId" placeholder="目标空间" style="width: 100%; margin-bottom: 12px">
      <el-option v-for="sp in writableSpaces" :key="sp.id" :label="sp.name" :value="sp.id" />
    </el-select>

    <el-breadcrumb separator="/" class="ly-move-crumbs">
      <el-breadcrumb-item v-for="c in crumbs" :key="c.id">
        <a href="javascript:void(0)" @click="jump(c.id)">{{ c.name }}</a>
      </el-breadcrumb-item>
    </el-breadcrumb>

    <div v-loading="loading" class="ly-move-list">
      <div v-if="!folders.length" class="ly-empty" style="padding: 32px 0">
        此处没有子目录，可直接{{ actionLabel }}到当前位置
      </div>
      <button v-for="f in folders" :key="f.id" class="ly-move-item" @click="enter(f)">
        <el-icon color="#f5a524"><Folder /></el-icon>
        <span>{{ f.name }}</span>
        <el-icon class="ly-muted"><ArrowRight /></el-icon>
      </button>
    </div>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">
        {{ actionLabel }}到此处
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.ly-move-crumbs {
  padding: 8px 2px;
}
.ly-move-list {
  height: 280px;
  overflow-y: auto;
  border: 1px solid var(--ly-border);
  border-radius: var(--ly-radius);
  padding: 6px;
}
.ly-move-item {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 10px;
  border: none;
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
  font-family: inherit;
  font-size: 14px;
  color: var(--ly-text);
  text-align: left;
}
.ly-move-item:hover {
  background: var(--ly-primary-soft);
}
.ly-move-item > span {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
