<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '@/api'
import type { APIKey, Space } from '@/api/types'
import { formatTime, relativeTime } from '@/utils/format'

const loading = ref(false)
const list = ref<APIKey[]>([])
const scopeCatalog = ref<Array<{ code: string; label: string; desc: string }>>([])
const spaces = ref<Space[]>([])

const dialog = reactive({ visible: false, submitting: false })
const form = reactive({
  name: '',
  scopes: [] as string[],
  spaceIds: [] as number[],
  expireDays: 90,
  remark: '',
})

/** 刚签发出来的明文密钥，只在这一刻存在于内存里。 */
const issued = ref<{ key: string; name: string } | null>(null)

async function load() {
  loading.value = true
  try {
    const res = await api.apiKeys()
    list.value = res.list
    scopeCatalog.value = res.scopes
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await load()
  spaces.value = await api.adminSpaces()
})

function openCreate() {
  Object.assign(form, { name: '', scopes: [], spaceIds: [], expireDays: 90, remark: '' })
  issued.value = null
  dialog.visible = true
}

/** 索引侧与查询侧应当用两把不同的钥匙，这里给两个一键预设。 */
function applyPreset(kind: 'indexer' | 'query') {
  if (kind === 'indexer') {
    form.name = form.name || '知识库索引'
    form.scopes = ['index.read', 'content.read']
  } else {
    form.name = form.name || '知识库查询'
    form.scopes = ['acl.check', 'user.read']
  }
}

async function submit() {
  if (!form.name.trim()) {
    ElMessage.warning('请为密钥起一个便于识别的名字')
    return
  }
  if (!form.scopes.length) {
    ElMessage.warning('请至少勾选一项能力')
    return
  }
  dialog.submitting = true
  try {
    const res = await api.createAPIKey({
      name: form.name.trim(),
      scopes: form.scopes,
      space_ids: form.spaceIds.length ? form.spaceIds : undefined,
      expire_days: form.expireDays || undefined,
      remark: form.remark || undefined,
    })
    issued.value = { key: res.key, name: res.record.name }
    await load()
  } finally {
    dialog.submitting = false
  }
}

async function copyKey() {
  if (!issued.value) return
  try {
    await navigator.clipboard.writeText(issued.value.key)
    ElMessage.success('密钥已复制')
  } catch {
    ElMessage.info('当前环境不支持自动复制，请手动选中')
  }
}

async function toggle(row: APIKey) {
  const next = !row.enabled
  await ElMessageBox.confirm(
    next
      ? `重新启用「${row.name}」？`
      : `停用后使用这把密钥的程序会立刻全部失败（下一个请求就被拒）。确定停用「${row.name}」吗？`,
    next ? '启用密钥' : '停用密钥',
    { type: 'warning', confirmButtonText: next ? '启用' : '停用', cancelButtonText: '取消' },
  )
  await api.setAPIKeyStatus(row.id, next)
  ElMessage.success(next ? '已启用' : '已停用')
  await load()
}

async function remove(row: APIKey) {
  await ElMessageBox.confirm(
    `删除后无法恢复，使用这把密钥的程序会立刻失败。确定删除「${row.name}」吗？`,
    '删除密钥',
    {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger',
    },
  )
  await api.deleteAPIKey(row.id)
  ElMessage.success('已删除')
  await load()
}

function scopeList(row: APIKey) {
  return row.scopes.split(',').filter(Boolean)
}

function scopeLabel(code: string) {
  return scopeCatalog.value.find((s) => s.code === code)?.label ?? code
}

function spaceLabel(row: APIKey) {
  if (!row.space_ids) return '全部空间'
  const ids = row.space_ids.split(',').filter(Boolean).map(Number)
  const names = ids.map((id) => spaces.value.find((s) => s.id === id)?.name ?? `#${id}`)
  return names.join('、')
}

function statusOf(row: APIKey) {
  if (!row.enabled) return { text: '已停用', cls: 'ly-tag ly-tag--danger' }
  if (row.expire_at && new Date(row.expire_at) < new Date()) {
    return { text: '已过期', cls: 'ly-tag ly-tag--warning' }
  }
  return { text: '生效中', cls: 'ly-tag ly-tag--success' }
}

/** content.read 是最重的一项：建全量索引就意味着能读到授权范围内的全部文件。 */
const heavyScopeChosen = computed(() => form.scopes.includes('content.read'))
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">API 密钥</h1>
        <p class="ly-page-desc">
          给外部程序（知识库索引、AI Agent、同步任务）用的凭证。能力由勾选项显式限定，默认什么都做不了。
        </p>
      </div>
      <el-button type="primary" :icon="'Plus'" @click="openCreate">签发密钥</el-button>
    </div>

    <!-- 安全提示放在最显眼处，而不是埋在文档里 -->
    <div class="ly-card ly-card-pad ly-keynote">
      <el-icon :size="18" color="#b5741a"><WarnTriangleFilled /></el-icon>
      <div>
        <p><strong>索引侧与查询侧请用两把不同的密钥。</strong></p>
        <p>
          索引程序需要「读取文件内容」——这等于能读到授权范围内的全部文件；
          而查询侧只需要「批量鉴权」，它读不到任何文件内容。
          两者混用一把密钥，泄露一次就是全量泄露。
        </p>
      </div>
    </div>

    <div class="ly-card">
      <el-table v-loading="loading" :data="list" row-key="id">
        <el-table-column label="名称" min-width="170">
          <template #default="{ row }">
            <div class="ly-key-name">{{ row.name }}</div>
            <div class="ly-muted ly-mono">{{ row.prefix }}…</div>
          </template>
        </el-table-column>
        <el-table-column label="能力" min-width="230">
          <template #default="{ row }">
            <span
              v-for="s in scopeList(row)"
              :key="s"
              class="ly-tag ly-key-scope"
              :class="{ 'ly-tag--warning': s === 'content.read' }"
            >
              {{ scopeLabel(s) }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="可访问空间" min-width="150">
          <template #default="{ row }">
            <span :class="{ 'ly-muted': !row.space_ids }">{{ spaceLabel(row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="最近使用" width="150">
          <template #default="{ row }">
            <span class="ly-muted">{{ row.last_used_at ? relativeTime(row.last_used_at) : '从未' }}</span>
            <div v-if="row.last_used_ip" class="ly-muted ly-mono" style="font-size: 11px">
              {{ row.last_used_ip }}
            </div>
          </template>
        </el-table-column>
        <el-table-column label="有效期" width="140">
          <template #default="{ row }">
            <span class="ly-muted">
              {{ row.expire_at ? formatTime(row.expire_at, false) : '长期' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <span :class="statusOf(row).cls">{{ statusOf(row).text }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="130" align="right">
          <template #default="{ row }">
            <el-button link size="small" @click="toggle(row)">
              {{ row.enabled ? '停用' : '启用' }}
            </el-button>
            <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">
            <el-icon :size="40" class="ly-muted"><Key /></el-icon>
            <p>还没有签发过密钥</p>
          </div>
        </template>
      </el-table>
    </div>

    <!-- 签发 -->
    <el-dialog v-model="dialog.visible" :title="issued ? '密钥已签发' : '签发 API 密钥'" width="620px" destroy-on-close>
      <template v-if="!issued">
        <el-form label-width="96px" label-position="left">
          <el-form-item label="名称" required>
            <el-input v-model="form.name" placeholder="便于日后辨认，例如：知识库索引" maxlength="64" />
          </el-form-item>

          <el-form-item label="快捷预设">
            <el-button size="small" @click="applyPreset('indexer')">索引侧</el-button>
            <el-button size="small" @click="applyPreset('query')">查询侧</el-button>
            <span class="ly-hint" style="margin-left: 8px">两侧应当各用一把</span>
          </el-form-item>

          <el-form-item label="能力" required>
            <div class="ly-scope-list">
              <label v-for="s in scopeCatalog" :key="s.code" class="ly-scope">
                <el-checkbox v-model="form.scopes" :value="s.code" />
                <div>
                  <strong>
                    {{ s.label }}
                    <span class="ly-mono ly-muted">{{ s.code }}</span>
                  </strong>
                  <span>{{ s.desc }}</span>
                </div>
              </label>
            </div>
          </el-form-item>

          <el-form-item v-if="heavyScopeChosen" label="">
            <div class="ly-warn">
              已勾选「读取文件内容」：这把密钥能读到授权空间内的<strong>全部文件</strong>。
              建议同时限定空间、设置有效期，并与查询侧密钥分开保管。
            </div>
          </el-form-item>

          <el-form-item label="限定空间">
            <el-select
              v-model="form.spaceIds"
              multiple
              clearable
              placeholder="不选表示全部空间"
              style="width: 100%"
            >
              <el-option v-for="sp in spaces" :key="sp.id" :label="sp.name" :value="sp.id" />
            </el-select>
            <div class="ly-hint">只做公共知识库的话，在这里选中公共空间即可。</div>
          </el-form-item>

          <el-form-item label="有效期">
            <el-radio-group v-model="form.expireDays">
              <el-radio-button :value="30">30 天</el-radio-button>
              <el-radio-button :value="90">90 天</el-radio-button>
              <el-radio-button :value="365">1 年</el-radio-button>
              <el-radio-button :value="0">长期</el-radio-button>
            </el-radio-group>
          </el-form-item>

          <el-form-item label="备注">
            <el-input v-model="form.remark" type="textarea" :rows="2" placeholder="选填，例如：跑在哪台机器上" />
          </el-form-item>
        </el-form>
      </template>

      <!-- 明文只出现这一次 -->
      <template v-else>
        <div class="ly-issued">
          <div class="ly-issued-warn">
            <el-icon :size="16"><WarnTriangleFilled /></el-icon>
            <!-- 整句必须裹在一个元素里：外层是 flex，散着放会让 <strong> 单独成为一个
                 flex item，句子被拆成三段各自折行，中间凭空多出空当。 -->
            <span>
              这是「{{ issued.name }}」的完整密钥，<strong>关闭后无法再次查看</strong>，请立即保存到你的密钥管理里。
            </span>
          </div>
          <div class="ly-issued-key">{{ issued.key }}</div>
          <ul class="ly-issued-tips">
            <li>放进环境变量，不要写进代码或提交到版本库</li>
            <li>请求时用 <code>X-API-Key</code> 或 <code>Authorization: Bearer</code> 头携带</li>
            <li>怀疑泄露时，在本页直接停用或删除，下一个请求就会被拒</li>
          </ul>
        </div>
      </template>

      <template #footer>
        <template v-if="!issued">
          <el-button @click="dialog.visible = false">取消</el-button>
          <el-button type="primary" :loading="dialog.submitting" @click="submit">签发</el-button>
        </template>
        <template v-else>
          <el-button @click="dialog.visible = false">我已保存</el-button>
          <el-button type="primary" @click="copyKey">复制密钥</el-button>
        </template>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.ly-keynote {
  display: flex;
  gap: 12px;
  margin-bottom: 18px;
  background: #fff8e8;
  border-color: #f6e3bd;
}
.ly-keynote p {
  margin: 0 0 4px;
  font-size: 13px;
  color: #8a6316;
  line-height: 1.8;
}

.ly-key-name {
  font-size: 14px;
}
.ly-key-scope {
  margin: 0 4px 2px 0;
}

.ly-hint {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  line-height: 1.7;
}

.ly-scope-list {
  display: grid;
  gap: 8px;
  width: 100%;
}
.ly-scope {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 9px 12px;
  border: 1px solid var(--ly-border);
  border-radius: 8px;
  cursor: pointer;
  background: var(--ly-surface-sunken);
}
.ly-scope:hover {
  border-color: var(--ly-primary-border);
}
.ly-scope > div {
  display: flex;
  flex-direction: column;
  line-height: 1.5;
}
.ly-scope strong {
  font-size: 13px;
  font-weight: 600;
}
.ly-scope strong .ly-mono {
  font-weight: 400;
  margin-left: 6px;
}
.ly-scope span {
  font-size: 12px;
  color: var(--ly-text-tertiary);
}

.ly-warn {
  width: 100%;
  padding: 10px 12px;
  border-radius: 8px;
  background: #fff6e6;
  border: 1px solid #fae3bb;
  color: #b5741a;
  font-size: 12.5px;
  line-height: 1.8;
}

.ly-issued-warn {
  display: flex;
  /* 文案会折成两行，图标跟着居中就会掉到句子中间，顶对齐更稳。 */
  align-items: flex-start;
  gap: 8px;
  line-height: 1.7;
  padding: 10px 12px;
  border-radius: 8px;
  background: #fff6e6;
  border: 1px solid #fae3bb;
  color: #b5741a;
  font-size: 13px;
  margin-bottom: 14px;
}
.ly-issued-warn .el-icon {
  /* 顶对齐后图标会略高于首行文字的视觉中线，压一点回来。 */
  margin-top: 3px;
  flex-shrink: 0;
}
.ly-issued-key {
  padding: 14px 16px;
  border-radius: 10px;
  background: var(--ly-primary-soft);
  border: 1px solid var(--ly-primary-border);
  color: var(--ly-primary);
  font-family: 'SFMono-Regular', Consolas, monospace;
  font-size: 14px;
  word-break: break-all;
  user-select: all;
}
.ly-issued-tips {
  margin: 16px 0 0;
  padding-left: 18px;
  font-size: 12.5px;
  color: var(--ly-text-tertiary);
  line-height: 2;
}
.ly-issued-tips code {
  background: var(--ly-surface-sunken);
  padding: 1px 5px;
  border-radius: 4px;
}
</style>
