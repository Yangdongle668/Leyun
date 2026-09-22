<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '@/api'
import type { FileNode, PermCode, Share } from '@/api/types'

const props = defineProps<{
  modelValue: boolean
  spaceId: number
  node: FileNode | null
}>()
const emit = defineEmits<{ 'update:modelValue': [boolean]; created: [] }>()

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const form = reactive({
  scope: 'internal' as 'internal' | 'public',
  perms: ['view', 'download'] as PermCode[],
  password: '',
  expireDays: 7,
  maxDownloads: 0,
})
const created = ref<Share | null>(null)
const submitting = ref(false)

const shareURL = computed(() =>
  created.value ? `${window.location.origin}/s/${created.value.code}` : '',
)

watch(visible, (open) => {
  if (open) {
    created.value = null
    form.scope = 'internal'
    form.perms = ['view', 'download']
    form.password = ''
    form.expireDays = 7
    form.maxDownloads = 0
  }
})

function randomPassword() {
  // 去掉易混字符，口头转述时不容易抄错。
  const chars = 'abcdefghjkmnpqrstuvwxyz23456789'
  let out = ''
  for (let i = 0; i < 4; i += 1) out += chars[Math.floor(Math.random() * chars.length)]
  form.password = out
}

async function submit() {
  if (!props.node) return
  submitting.value = true
  try {
    created.value = await api.createShare({
      space_id: props.spaceId,
      node_id: props.node.id,
      scope: form.scope,
      perms: form.perms,
      password: form.password || undefined,
      expire_days: form.expireDays || undefined,
      max_downloads: form.maxDownloads || undefined,
    })
    emit('created')
  } finally {
    submitting.value = false
  }
}

async function copyLink() {
  const text = form.password
    ? `${shareURL.value}\n提取码：${form.password}`
    : shareURL.value
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success('链接已复制')
  } catch {
    // clipboard 在非安全上下文（裸 http）下不可用，退回手动选中。
    ElMessage.info('当前环境不支持自动复制，请手动选中链接')
  }
}
</script>

<template>
  <el-dialog v-model="visible" width="520px" destroy-on-close>
    <template #header>
      <div class="ly-dlg-head">
        <span>{{ created ? '分享已创建' : '创建分享' }}</span>
        <span v-if="node" class="ly-dlg-sub">{{ node.name }}</span>
      </div>
    </template>

    <!-- 创建前：参数表单 -->
    <template v-if="!created">
      <el-form label-width="84px" label-position="left">
        <el-form-item label="可见范围">
          <el-radio-group v-model="form.scope">
            <el-radio value="internal">仅企业内部</el-radio>
            <el-radio value="public">任何人可访问</el-radio>
          </el-radio-group>
          <div class="ly-hint">
            {{
              form.scope === 'internal'
                ? '需要登录乐云后才能打开，适合内部流转。'
                : '拿到链接的人无需登录即可访问，建议搭配提取码与有效期。'
            }}
          </div>
        </el-form-item>

        <el-form-item label="访客权限">
          <el-checkbox-group v-model="form.perms">
            <el-checkbox value="view" disabled>查看</el-checkbox>
            <el-checkbox value="download">下载</el-checkbox>
            <el-checkbox v-if="node?.is_dir" value="upload">上传</el-checkbox>
          </el-checkbox-group>
          <div class="ly-hint">分享出去的权限不会超过你自己在该位置的权限。</div>
        </el-form-item>

        <el-form-item label="提取码">
          <div class="ly-inline">
            <el-input v-model="form.password" placeholder="留空表示无需提取码" maxlength="16" clearable />
            <el-button @click="randomPassword">随机生成</el-button>
          </div>
        </el-form-item>

        <el-form-item label="有效期">
          <el-radio-group v-model="form.expireDays">
            <el-radio-button :value="1">1 天</el-radio-button>
            <el-radio-button :value="7">7 天</el-radio-button>
            <el-radio-button :value="30">30 天</el-radio-button>
            <el-radio-button :value="0">长期</el-radio-button>
          </el-radio-group>
        </el-form-item>

        <el-form-item label="下载次数">
          <el-input-number v-model="form.maxDownloads" :min="0" :max="100000" style="width: 160px" />
          <span class="ly-hint" style="margin-left: 10px">0 表示不限制</span>
        </el-form-item>
      </el-form>
    </template>

    <!-- 创建后：链接与提取码 -->
    <template v-else>
      <div class="ly-share-result">
        <div class="ly-share-link">{{ shareURL }}</div>
        <div v-if="form.password" class="ly-share-pwd">
          提取码 <strong>{{ form.password }}</strong>
        </div>
        <ul class="ly-share-meta">
          <li>可见范围：{{ form.scope === 'public' ? '任何人可访问' : '仅企业内部' }}</li>
          <li>访客权限：{{ form.perms.includes('download') ? '可查看、可下载' : '仅可查看' }}</li>
          <li>有效期：{{ form.expireDays ? `${form.expireDays} 天` : '长期有效' }}</li>
          <li v-if="form.maxDownloads">下载上限：{{ form.maxDownloads }} 次</li>
        </ul>
      </div>
    </template>

    <!-- 具名插槽必须是组件的直接子节点，不能塞进上面的 v-if 分支里 -->
    <template #footer>
      <template v-if="!created">
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">创建分享链接</el-button>
      </template>
      <template v-else>
        <el-button @click="visible = false">完成</el-button>
        <el-button type="primary" @click="copyLink">复制链接</el-button>
      </template>
    </template>
  </el-dialog>
</template>

<style scoped>
.ly-dlg-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
}
.ly-dlg-sub {
  font-size: 13px;
  font-weight: 400;
  color: var(--ly-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 300px;
}
.ly-hint {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  line-height: 1.7;
}
.ly-inline {
  display: flex;
  gap: 8px;
  width: 100%;
}

.ly-share-result {
  padding: 4px 0 8px;
}
.ly-share-link {
  padding: 12px 14px;
  border-radius: 10px;
  background: var(--ly-primary-soft);
  border: 1px solid var(--ly-primary-border);
  color: var(--ly-primary);
  font-family: 'SFMono-Regular', Consolas, monospace;
  font-size: 13px;
  word-break: break-all;
  user-select: all;
}
.ly-share-pwd {
  margin-top: 12px;
  font-size: 14px;
  color: var(--ly-text-secondary);
}
.ly-share-pwd strong {
  font-size: 20px;
  letter-spacing: 4px;
  color: var(--ly-text);
  margin-left: 6px;
}
.ly-share-meta {
  margin: 16px 0 0;
  padding: 0;
  list-style: none;
  font-size: 13px;
  color: var(--ly-text-tertiary);
  line-height: 2;
}
</style>
