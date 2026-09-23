<script lang="ts">
// 类型放在普通 script 块里：<script setup> 不允许写 export。
/** 一条冲突：要上传的文件，和目标位置里那个同名的老文件。 */
export interface UploadConflict {
  name: string
  /** 新文件大小（字节）。 */
  newSize: number
  /** 老文件大小（字节）。 */
  oldSize: number
  /** 老文件最后修改时间，用来帮着判断哪个新。 */
  oldTime?: string
  /** 老文件是不是目录——同名的是目录就只能改名，不能覆盖。 */
  oldIsDir: boolean
  /** 对这个老文件有没有编辑权；没有就不给选"替换"。 */
  canReplace: boolean
}
</script>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { ConflictMode } from '@/api/types'
import { humanSize, relativeTime } from '@/utils/format'
import FileIcon from '@/components/FileIcon.vue'

const props = defineProps<{
  modelValue: boolean
  conflicts: UploadConflict[]
}>()
const emit = defineEmits<{
  'update:modelValue': [boolean]
  /** 用户选完了：mode 是处理方式，cancel 为 true 表示整批都不传了。 */
  resolve: [{ mode: ConflictMode; cancel: boolean }]
}>()

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const mode = ref<ConflictMode>('rename')
// 点"确定"会把 visible 置 false，从而又触发一次 el-dialog 的 close 事件。
// 用它保证一次开关只往外抛一个结果。
const decided = ref(false)

// 有一个不能替换（同名的是目录，或者对老文件没有编辑权）就不提供"替换"，
// 免得用户选了以后一部分文件悄悄走了改名的路子，结果跟他以为的不一样。
const replaceBlocked = computed(() => props.conflicts.some((c) => c.oldIsDir || !c.canReplace))

/** 拿第一个冲突名算出改名后的样子，比写死一个"报价单(1).xlsx"更有说服力。 */
const renameExample = computed(() => {
  const name = props.conflicts[0]?.name ?? ''
  const dot = name.lastIndexOf('.')
  return dot > 0 ? `${name.slice(0, dot)}(1)${name.slice(dot)}` : `${name}(1)`
})
const blockedReason = computed(() => {
  if (props.conflicts.some((c) => c.oldIsDir)) return '有同名的是文件夹，只能改名放进去'
  return '你对其中的文件没有编辑权限，只能改名上传'
})

watch(visible, (open) => {
  if (!open) return
  // 每次重新打开都回到默认选项：默认不动别人的文件，最保守。
  mode.value = 'rename'
  decided.value = false
})

function confirm() {
  if (decided.value) return
  decided.value = true
  emit('resolve', { mode: mode.value, cancel: false })
  visible.value = false
}

/** 右上角的叉、点遮罩、按 Esc 都走这里：当成"这批都别传了"。 */
function cancel() {
  if (decided.value) return
  decided.value = true
  emit('resolve', { mode: 'skip', cancel: true })
  visible.value = false
}
</script>

<template>
  <el-dialog
    v-model="visible"
    title="目标位置已有同名文件"
    width="560px"
    destroy-on-close
    :close-on-click-modal="false"
    @close="cancel"
  >
    <p class="ly-conflict-lead">
      <template v-if="conflicts.length === 1">这个文件在目标位置已经存在，要怎么处理？</template>
      <template v-else>这 {{ conflicts.length }} 个文件在目标位置已经存在，要怎么处理？</template>
    </p>

    <div class="ly-conflict-list">
      <div v-for="c in conflicts" :key="c.name" class="ly-conflict-row">
        <FileIcon :name="c.name" :is-dir="c.oldIsDir" />
        <div class="ly-conflict-name" :title="c.name">{{ c.name }}</div>
        <div class="ly-conflict-size">
          <span>新 {{ humanSize(c.newSize) }}</span>
          <span class="ly-conflict-old">
            原 {{ c.oldIsDir ? '文件夹' : humanSize(c.oldSize) }}
            <template v-if="c.oldTime">· {{ relativeTime(c.oldTime) }}</template>
          </span>
        </div>
      </div>
    </div>

    <el-radio-group v-model="mode" class="ly-conflict-options">
      <el-radio value="rename">
        <strong>保留两者</strong>
        <span class="ly-conflict-hint">
          新文件自动改名成「{{ renameExample }}」{{ conflicts.length > 1 ? '，依此类推' : '' }}，原文件不动
        </span>
      </el-radio>
      <el-radio value="replace" :disabled="replaceBlocked">
        <strong>替换原文件</strong>
        <span class="ly-conflict-hint">
          {{
            replaceBlocked
              ? blockedReason
              : '内容写进原文件，版本号加一，原有的权限和分享链接继续有效'
          }}
        </span>
      </el-radio>
      <el-radio value="skip">
        <strong>跳过这些文件</strong>
        <span class="ly-conflict-hint">只上传其余不重名的文件</span>
      </el-radio>
    </el-radio-group>

    <template #footer>
      <el-button @click="cancel">全部取消</el-button>
      <el-button type="primary" @click="confirm">确定</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.ly-conflict-lead {
  margin: 0 0 12px;
  color: var(--ly-text-secondary);
}
.ly-conflict-list {
  max-height: 200px;
  overflow-y: auto;
  border: 1px solid var(--ly-border);
  border-radius: var(--ly-radius);
  padding: 4px 0;
}
.ly-conflict-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
}
.ly-conflict-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-conflict-size {
  display: flex;
  gap: 10px;
  font-size: 12px;
  color: var(--ly-text-tertiary);
  white-space: nowrap;
}
.ly-conflict-old {
  color: var(--ly-text-tertiary);
  opacity: 0.75;
}
.ly-conflict-options {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 4px;
  margin-top: 16px;
}
/* 选项要能换行放下说明文字，Element 默认是单行居中的。 */
.ly-conflict-options :deep(.el-radio) {
  height: auto;
  align-items: flex-start;
  padding: 8px 0;
  margin-right: 0;
}
.ly-conflict-options :deep(.el-radio__label) {
  display: flex;
  flex-direction: column;
  gap: 2px;
  white-space: normal;
  line-height: 1.5;
}
.ly-conflict-hint {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  font-weight: 400;
}
</style>
