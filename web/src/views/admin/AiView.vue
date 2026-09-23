<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Connection, Refresh, Search, WarnTriangleFilled } from '@element-plus/icons-vue'

import { api, type AIConfig, type KBDoc, type KBStatus, type Space } from '@/api'
import { humanSize } from '@/utils/format'

const loading = ref(true)
const saving = ref(false)
const testing = ref(false)
const status = ref<KBStatus>()
const problems = ref<KBDoc[]>([])
const spaces = ref<Space[]>([])
const extensions = ref<string[]>([])

/** 密钥已配置过时，输入框留空表示"不改"。 */
const apiKeyInput = ref('')
const apiKeySet = ref(false)
const apiKeyMask = ref('')

const form = reactive<AIConfig>({
  enabled: false,
  base_url: '',
  api_key_mask: '',
  api_key_set: false,
  chat_model: '',
  embed_model: '',
  embed_dim: 0,
  embed_batch: 0,
  chunk_size: 700,
  chunk_overlap: 80,
  top_k: 6,
  space_ids: [],
  include_personal: false,
  max_file_size: 32 * 1024 * 1024,
})

const testResult = ref<{ ok: boolean; lines: string[] }>()
const preview = ref<Array<{ name: string; path_names: string[]; score: number; preview: string }>>([])
const previewQuery = ref('')
const previewing = ref(false)

let timer: number | undefined

const progress = computed(() => {
  const s = status.value
  if (!s || s.total === 0) return 0
  return Math.round(((s.done + s.skipped + s.failed) / s.total) * 100)
})

// 常见供应商的现成参数。照着填最容易出错的就是维度对不上，给好默认值省一轮排查。
const presets = [
  { label: '阿里云百炼（通义）', base: 'https://dashscope.aliyuncs.com/compatible-mode/v1', chat: 'qwen-plus', embed: 'text-embedding-v3', dim: 1024 },
  { label: 'DeepSeek + 本地向量', base: 'https://api.deepseek.com/v1', chat: 'deepseek-chat', embed: 'bge-m3', dim: 1024 },
  { label: '智谱 GLM', base: 'https://open.bigmodel.cn/api/paas/v4', chat: 'glm-4-plus', embed: 'embedding-3', dim: 2048 },
  { label: '本地 Ollama', base: 'http://127.0.0.1:11434/v1', chat: 'qwen2.5:14b', embed: 'bge-m3', dim: 1024 },
]

onMounted(async () => {
  await load()
  try {
    spaces.value = await api.adminSpaces()
  } catch {
    /* 空间列表拉不到不影响配置 */
  }
  // 索引在后台慢慢跑，页面开着就定时刷新进度。
  timer = window.setInterval(refreshStatus, 5000)
})

onBeforeUnmount(() => {
  if (timer) window.clearInterval(timer)
})

async function load() {
  loading.value = true
  try {
    const res = await api.aiSettings()
    Object.assign(form, res.config)
    apiKeySet.value = res.config.api_key_set
    apiKeyMask.value = res.config.api_key_mask
    apiKeyInput.value = ''
    status.value = res.status
    extensions.value = res.extensions
  } catch (err) {
    ElMessage.error((err as Error).message || '读取配置失败')
  } finally {
    loading.value = false
  }
}

async function refreshStatus() {
  try {
    const res = await api.kbIndexStatus()
    status.value = res.status
    problems.value = res.problems
  } catch {
    /* 轮询失败不打扰用户 */
  }
}

function applyPreset(p: (typeof presets)[number]) {
  form.base_url = p.base
  form.chat_model = p.chat
  form.embed_model = p.embed
  form.embed_dim = p.dim
}

/** 密钥留空表示不改；要清空得显式传 "-"。 */
function keyPayload(): string | undefined {
  const v = apiKeyInput.value.trim()
  if (v) return v
  return undefined
}

async function save() {
  if (form.enabled && !form.base_url.trim()) {
    ElMessage.warning('请先填写服务地址')
    return
  }
  saving.value = true
  try {
    const res = await api.updateAISettings({
      enabled: form.enabled,
      base_url: form.base_url,
      api_key: keyPayload(),
      chat_model: form.chat_model,
      embed_model: form.embed_model,
      embed_dim: form.embed_dim,
      embed_batch: form.embed_batch,
      chunk_size: form.chunk_size,
      chunk_overlap: form.chunk_overlap,
      top_k: form.top_k,
      space_ids: form.space_ids,
      include_personal: form.include_personal,
      max_file_size: form.max_file_size,
    })
    apiKeySet.value = res.config.api_key_set
    apiKeyMask.value = res.config.api_key_mask
    apiKeyInput.value = ''
    ElMessage.success('已保存')

    if (res.need_reindex) {
      // 换了向量模型或维度，旧向量与新查询不在同一个语义空间里，比出来的相似度没有意义。
      try {
        await ElMessageBox.confirm(res.reindex_hint + ' 现在就重建吗？', '需要重建索引', {
          type: 'warning',
          confirmButtonText: '立即重建',
          cancelButtonText: '稍后手动重建',
        })
        await reindex()
      } catch {
        /* 用户选择稍后 */
      }
    }
    await refreshStatus()
  } catch (err) {
    ElMessage.error((err as Error).message || '保存失败')
  } finally {
    saving.value = false
  }
}

async function test() {
  testing.value = true
  testResult.value = undefined
  try {
    const res = await api.testAIConnection({
      base_url: form.base_url,
      api_key: keyPayload(),
      chat_model: form.chat_model,
      embed_model: form.embed_model,
      embed_dim: form.embed_dim,
    })
    const lines: string[] = []
    if (res.embed_ok) lines.push(`向量模型可用，返回 ${res.embed_dim} 维`)
    if (res.dim_mismatch) lines.push(res.dim_mismatch)
    if (res.chat_ok) lines.push(`对话模型可用，回复：${res.chat_reply}`)
    testResult.value = { ok: true, lines }
    // 维度以实际返回的为准，省得管理员自己去查文档。
    if (res.embed_dim && res.embed_dim !== form.embed_dim) {
      form.embed_dim = res.embed_dim
      lines.push(`已把维度自动改成 ${res.embed_dim}，记得保存。`)
    }
  } catch (err) {
    testResult.value = { ok: false, lines: [(err as Error).message || '连接失败'] }
  } finally {
    testing.value = false
  }
}

async function reindex() {
  try {
    await ElMessageBox.confirm(
      '将清空现有索引并重新处理全部文档。重建期间问答质量会下降，且会重新消耗一遍向量接口额度。',
      '重建索引',
      { type: 'warning', confirmButtonText: '确认重建', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await api.kbReindex()
    ElMessage.success('已开始重建，进度见下方')
    await refreshStatus()
  } catch (err) {
    ElMessage.error((err as Error).message || '重建失败')
  }
}

async function retry() {
  try {
    const res = await api.kbRetryFailed()
    ElMessage.success(`已把 ${res.retried} 份文档放回队列`)
    await refreshStatus()
  } catch (err) {
    ElMessage.error((err as Error).message || '操作失败')
  }
}

async function runPreview() {
  const q = previewQuery.value.trim()
  if (!q) return
  previewing.value = true
  try {
    preview.value = await api.kbSearchPreview(q)
  } catch (err) {
    ElMessage.error((err as Error).message || '检索失败')
  } finally {
    previewing.value = false
  }
}

function statusLabel(s: string) {
  return { done: '已索引', pending: '排队中', indexing: '处理中', skipped: '已跳过', failed: '失败' }[s] || s
}
function statusType(s: string) {
  return { done: 'success', failed: 'danger', skipped: 'info' }[s] || 'warning'
}
</script>

<template>
  <div class="ly-page" v-loading="loading">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">智能问答</h1>
        <p class="ly-page-desc">
          接入大模型后，全员可以用自然语言在云盘里找资料。
          <strong>检索结果始终按提问人本人的权限过滤</strong>，不会因为接了 AI 就绕开部门授权。
        </p>
      </div>
    </div>

    <div class="ly-card ly-card-pad ly-ai-note">
      <el-icon :size="18" color="#b5741a"><WarnTriangleFilled /></el-icon>
      <div>
        <p><strong>开启前请知悉：索引是数据的第二份拷贝。</strong></p>
        <p>
          文档会被抽成文本、切块后送到你配置的大模型服务做向量化，向量存在本地数据库里。
          用公有云的模型服务，意味着文档内容会离开这台服务器；
          对保密要求高的场景，请把服务地址指向内网自建的模型（Ollama、Xinference、vLLM 均可）。
        </p>
      </div>
    </div>

    <!-- 连接配置 -->
    <div class="ly-card ly-card-pad">
      <h2 class="ly-section-title">模型连接</h2>
      <el-form label-width="120px" label-position="left">
        <el-form-item label="启用智能问答">
          <el-switch v-model="form.enabled" />
          <span class="ly-hint" style="margin-left: 10px">关掉后索引暂停，用户侧的问答入口也会隐藏</span>
        </el-form-item>

        <el-form-item label="快捷预设">
          <el-button v-for="p in presets" :key="p.label" size="small" @click="applyPreset(p)">
            {{ p.label }}
          </el-button>
        </el-form-item>

        <el-form-item label="服务地址" required>
          <el-input v-model="form.base_url" placeholder="https://api.example.com/v1" />
          <div class="ly-hint">
            需兼容 OpenAI 接口。填到 /v1 即可，系统会自动补全；通义、智谱、DeepSeek
            以及自建的 Ollama / Xinference / vLLM 都支持。
          </div>
        </el-form-item>

        <el-form-item label="API Key">
          <el-input
            v-model="apiKeyInput"
            type="password"
            show-password
            :placeholder="apiKeySet ? `已配置（${apiKeyMask}），留空表示不修改` : '粘贴供应商给的密钥'"
          />
          <div class="ly-hint">
            保存后只回显末四位，服务端也不会再把它交给浏览器。自建模型不需要密钥时可以留空。
          </div>
        </el-form-item>

        <el-form-item label="对话模型">
          <el-input v-model="form.chat_model" placeholder="用于生成回答，例如 qwen-plus" />
        </el-form-item>

        <el-form-item label="向量模型" required>
          <el-input v-model="form.embed_model" placeholder="用于建索引，例如 text-embedding-v3" />
        </el-form-item>

        <el-form-item label="向量维度" required>
          <el-input-number v-model="form.embed_dim" :min="0" :max="8192" :step="256" />
          <span class="ly-hint" style="margin-left: 10px">
            点「测试连接」可自动探测。改动后必须重建索引。
          </span>
        </el-form-item>

        <el-form-item label="单次条数">
          <el-input-number v-model="form.embed_batch" :min="0" :max="2048" :step="1" />
          <span class="ly-hint" style="margin-left: 10px">
            一次向量请求送几段文本。留 0 表示自动：撞上服务商的上限会自己退让。
            知道自家上限的可以直接填（通义千问部分模型只收 10 条，OpenAI 可以上千）。
          </span>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">保存</el-button>
          <el-button :icon="Connection" :loading="testing" @click="test">测试连接</el-button>
        </el-form-item>

        <el-form-item v-if="testResult" label="">
          <div class="ly-ai-test" :class="testResult.ok ? 'is-ok' : 'is-bad'">
            <p v-for="(l, i) in testResult.lines" :key="i">{{ l }}</p>
          </div>
        </el-form-item>
      </el-form>
    </div>

    <!-- 索引范围 -->
    <div class="ly-card ly-card-pad">
      <h2 class="ly-section-title">索引范围</h2>
      <el-form label-width="120px" label-position="left">
        <el-form-item label="参与索引的空间">
          <el-select v-model="form.space_ids" multiple clearable placeholder="不选表示全部非个人空间" style="width: 100%">
            <el-option v-for="s in spaces" :key="s.id" :label="s.name" :value="s.id" />
          </el-select>
        </el-form-item>

        <el-form-item label="包含个人空间">
          <el-switch v-model="form.include_personal" />
          <span class="ly-hint" style="margin-left: 10px">
            默认关闭。个人空间是员工的私人区域，检索时虽然会被权限挡住，但没必要多一份向量副本。
          </span>
        </el-form-item>

        <el-form-item label="单文件上限">
          <el-input-number
            :model-value="Math.round(form.max_file_size / 1024 / 1024)"
            :min="1"
            :max="500"
            @update:model-value="(v: number) => (form.max_file_size = v * 1024 * 1024)"
          />
          <span class="ly-hint" style="margin-left: 8px">MB，超过的直接跳过</span>
        </el-form-item>

        <el-form-item label="切块大小">
          <el-input-number v-model="form.chunk_size" :min="120" :max="4000" :step="100" />
          <el-input-number v-model="form.chunk_overlap" :min="0" :max="500" :step="20" style="margin-left: 10px" />
          <span class="ly-hint" style="margin-left: 8px">字符数 / 相邻块重叠</span>
        </el-form-item>

        <el-form-item label="每次引用条数">
          <el-input-number v-model="form.top_k" :min="1" :max="20" />
          <span class="ly-hint" style="margin-left: 8px">送进模型的资料片段数</span>
        </el-form-item>

        <el-form-item label="可索引类型">
          <div class="ly-hint">
            {{ extensions.slice().sort().join('、') }}
          </div>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">保存</el-button>
        </el-form-item>
      </el-form>
    </div>

    <!-- 索引进度 -->
    <div class="ly-card ly-card-pad">
      <div class="ly-ai-head">
        <h2 class="ly-section-title" style="margin: 0">索引进度</h2>
        <div>
          <el-button size="small" :icon="Refresh" @click="refreshStatus">刷新</el-button>
          <el-button size="small" @click="retry">重试失败项</el-button>
          <el-button size="small" type="danger" plain @click="reindex">重建全部</el-button>
        </div>
      </div>

      <div v-if="status" class="ly-ai-stats">
        <div class="ly-stat"><span>文档总数</span><strong>{{ status.total }}</strong></div>
        <div class="ly-stat"><span>已索引</span><strong>{{ status.done }}</strong></div>
        <div class="ly-stat"><span>排队中</span><strong>{{ status.pending }}</strong></div>
        <div class="ly-stat"><span>已跳过</span><strong>{{ status.skipped }}</strong></div>
        <div class="ly-stat"><span>失败</span><strong>{{ status.failed }}</strong></div>
        <div class="ly-stat"><span>文本片段</span><strong>{{ status.chunks }}</strong></div>
        <div class="ly-stat"><span>索引内存</span><strong>{{ humanSize(status.indexed_mem) }}</strong></div>
      </div>

      <el-progress
        v-if="status && status.total > 0"
        :percentage="progress"
        :status="status.indexing ? undefined : 'success'"
        style="margin-top: 12px"
      />
      <p v-if="status?.indexing" class="ly-hint" style="margin-top: 8px">
        正在后台索引……大文档需要一些时间，可以离开本页。
      </p>
      <p v-else-if="status && status.pending > 0" class="ly-hint" style="margin-top: 8px">
        队列里还有 {{ status.pending }} 份等待处理，索引每两分钟自动推进一轮。
      </p>

      <template v-if="problems.length">
        <h3 class="ly-ai-sub">未能入库的文档</h3>
        <el-table :data="problems" size="small" max-height="320">
          <el-table-column prop="name" label="文件" min-width="200" show-overflow-tooltip />
          <el-table-column prop="ext" label="类型" width="80" />
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="statusType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="err" label="原因" min-width="260" show-overflow-tooltip />
        </el-table>
        <p class="ly-hint" style="margin-top: 8px">
          扫描件 PDF 没有文字层，本系统不做 OCR，这类文件会被跳过——它们没有进知识库，检索不到属于正常。
        </p>
      </template>
    </div>

    <!-- 检索自检 -->
    <div class="ly-card ly-card-pad">
      <h2 class="ly-section-title">检索自检</h2>
      <p class="ly-hint" style="margin-bottom: 12px">
        用<strong>你自己的身份</strong>试一次检索，确认索引和权限过滤符合预期。
        这里只显示命中的文件与片段开头，不是别人视角的结果。
      </p>
      <div class="ly-ai-preview-bar">
        <el-input
          v-model="previewQuery"
          placeholder="输入一个问题试试"
          @keyup.enter="runPreview"
        />
        <el-button type="primary" :icon="Search" :loading="previewing" @click="runPreview">检索</el-button>
      </div>
      <el-table v-if="preview.length" :data="preview" size="small" style="margin-top: 12px">
        <el-table-column prop="name" label="文件" width="200" show-overflow-tooltip />
        <el-table-column label="位置" width="200" show-overflow-tooltip>
          <template #default="{ row }">{{ row.path_names?.join(' / ') }}</template>
        </el-table-column>
        <el-table-column label="相似度" width="90">
          <template #default="{ row }">{{ row.score?.toFixed(3) }}</template>
        </el-table-column>
        <el-table-column prop="preview" label="片段" min-width="240" show-overflow-tooltip />
      </el-table>
    </div>
  </div>
</template>

<style scoped>
.ly-ai-note {
  display: flex;
  gap: 12px;
  margin-bottom: 18px;
  background: #fff8e8;
  border-color: #f6e3bd;
}
.ly-ai-note p {
  margin: 0 0 4px;
  font-size: 13px;
  color: #8a6316;
  line-height: 1.8;
}
.ly-ai-note .el-icon {
  flex-shrink: 0;
  margin-top: 3px;
}
.ly-section-title {
  font-size: 15px;
  margin: 0 0 16px;
}
.ly-card-pad + .ly-card-pad {
  margin-top: 16px;
}
.ly-ai-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 14px;
}
.ly-ai-stats {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
  gap: 10px;
}
.ly-ai-stats .ly-stat {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--ly-surface-sunken);
}
.ly-ai-stats .ly-stat span {
  font-size: 12px;
  color: var(--ly-text-tertiary);
}
.ly-ai-stats .ly-stat strong {
  font-size: 18px;
}
.ly-ai-sub {
  font-size: 14px;
  margin: 20px 0 10px;
}
.ly-ai-test {
  width: 100%;
  padding: 10px 12px;
  border-radius: 8px;
  font-size: 13px;
  line-height: 1.8;
}
.ly-ai-test p {
  margin: 0;
}
.ly-ai-test.is-ok {
  background: #f0f9eb;
  border: 1px solid #d8ecc8;
  color: #4e8a2d;
}
.ly-ai-test.is-bad {
  background: #fef0f0;
  border: 1px solid #fbc4c4;
  color: #c04646;
}
.ly-ai-preview-bar {
  display: flex;
  gap: 10px;
}
</style>
