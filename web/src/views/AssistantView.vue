<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ChatLineRound, Delete, Plus, Promotion, Document } from '@element-plus/icons-vue'

import { api, type KBCitation, type KBConversation } from '@/api'
import { askKB } from '@/api/kbstream'
import { useUserStore } from '@/stores/user'
import { renderMarkdown } from '@/utils/markdown'

/** 界面上的一条消息。 */
interface Turn {
  role: 'user' | 'assistant'
  content: string
  citations: KBCitation[]
  /** 生成中的那一条，用来显示光标与"停止"按钮。 */
  streaming?: boolean
  status?: string
}

const router = useRouter()
const userStore = useUserStore()

const ready = ref<boolean | null>(null)
const convs = ref<KBConversation[]>([])
const activeConv = ref(0)
const turns = ref<Turn[]>([])
const question = ref('')
const sending = ref(false)
const loadingHistory = ref(false)
const bodyRef = ref<HTMLElement>()
let abort: (() => void) | null = null

const canSend = computed(() => question.value.trim().length > 0 && !sending.value)

const examples = [
  '公司的报销标准是什么？',
  '年假怎么计算？',
  '上季度的项目进展有哪些结论？',
]

onMounted(async () => {
  try {
    const st = await api.kbStatus()
    ready.value = st.can_chat
    if (st.can_chat) await loadConvs()
  } catch {
    ready.value = false
  }
})

onBeforeUnmount(() => abort?.())

async function loadConvs() {
  try {
    convs.value = await api.kbConversations()
  } catch {
    /* 列表拉不到不影响提问 */
  }
}

async function openConv(id: number) {
  if (id === activeConv.value) return
  abort?.()
  sending.value = false
  loadingHistory.value = true
  try {
    const msgs = await api.kbMessages(id)
    turns.value = msgs.map((m) => ({
      role: m.role,
      content: m.content,
      // 引用是当时算出来存下的，不重算——权限后来变了不该改写历史记录。
      citations: parseCitations(m.citations),
    }))
    activeConv.value = id
    scrollToBottom()
  } catch (err) {
    ElMessage.error((err as Error).message || '读取会话失败')
  } finally {
    loadingHistory.value = false
  }
}

function parseCitations(raw?: string): KBCitation[] {
  if (!raw) return []
  try {
    const v = JSON.parse(raw)
    return Array.isArray(v) ? (v as KBCitation[]) : []
  } catch {
    return []
  }
}

function newConv() {
  abort?.()
  sending.value = false
  activeConv.value = 0
  turns.value = []
  question.value = ''
}

async function removeConv(conv: KBConversation) {
  try {
    await ElMessageBox.confirm(`确定删除会话「${conv.title}」？`, '删除会话', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.deleteKBConversation(conv.id)
    if (activeConv.value === conv.id) newConv()
    await loadConvs()
    ElMessage.success('已删除')
  } catch (err) {
    ElMessage.error((err as Error).message || '删除失败')
  }
}

function send() {
  const q = question.value.trim()
  if (!q || sending.value) return

  question.value = ''
  sending.value = true
  turns.value.push({ role: 'user', content: q, citations: [] })
  const reply: Turn = { role: 'assistant', content: '', citations: [], streaming: true, status: '正在检索……' }
  turns.value.push(reply)
  scrollToBottom()

  abort = askKB(
    { question: q, conv_id: activeConv.value || undefined },
    {
      onStatus: (text) => {
        reply.status = text
      },
      onCitations: (items) => {
        // 引用先到：用户能立刻看到找到了哪几份文件，而不是盯着空白等模型开口。
        reply.citations = items
        scrollToBottom()
      },
      onDelta: (text) => {
        reply.status = ''
        reply.content += text
        scrollToBottom()
      },
      onDone: async (convID) => {
        reply.streaming = false
        reply.status = ''
        sending.value = false
        if (!activeConv.value) {
          activeConv.value = convID
          await loadConvs()
        }
      },
      onError: (msg) => {
        reply.streaming = false
        reply.status = ''
        sending.value = false
        if (!reply.content) {
          turns.value.pop()
        }
        ElMessage.error(msg)
      },
    },
  )
}

function stop() {
  abort?.()
  abort = null
  sending.value = false
  const last = turns.value[turns.value.length - 1]
  if (last?.streaming) {
    last.streaming = false
    last.status = ''
    if (!last.content) last.content = '（已停止）'
  }
}

function onKeydown(e: KeyboardEvent) {
  // Enter 发送，Shift+Enter 换行——和常见的聊天框一致。
  if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
    e.preventDefault()
    send()
  }
}

function openFile(c: KBCitation) {
  router.push({ name: 'files', params: { spaceId: String(c.space_id) } })
}

/**
 * 引用里显示文件所在的目录。
 *
 * path_names 的最后一项是文件自己，而文件名已经单独显示了，
 * 直接整串拼出来会变成"研发架构方案.txt 研发架构方案.txt"。
 * 文件就在空间根目录时没有中间目录，返回空串，模板里也就不渲染。
 */
function whereOf(c: KBCitation) {
  const parts = c.path_names ?? []
  return parts.length > 1 ? parts.slice(0, -1).join(' / ') : ''
}

function scrollToBottom() {
  void nextTick(() => {
    const el = bodyRef.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

watch(turns, scrollToBottom, { deep: true })
</script>

<template>
  <div class="ly-page ly-assistant">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">智能问答</h1>
        <p class="ly-page-desc">
          基于公司云盘里的文档回答问题。<strong>检索结果按你本人的权限过滤</strong>——
          你看不到的文件，不会出现在回答和引用里。
        </p>
      </div>
    </div>

    <!-- 还没配好：普通成员看到的是"请联系管理员"，超管直接给入口 -->
    <div v-if="ready === false" class="ly-card ly-card-pad ly-kb-off">
      <el-icon :size="40" class="ly-muted"><ChatLineRound /></el-icon>
      <h3>智能问答尚未启用</h3>
      <p class="ly-muted">
        需要超级管理员在后台填写大模型的连接信息，并等待文档索引建立完成。
      </p>
      <el-button v-if="userStore.isSuperAdmin" type="primary" @click="router.push('/admin/ai')">
        去配置
      </el-button>
    </div>

    <div v-else-if="ready" class="ly-kb">
      <!-- 会话列表 -->
      <aside class="ly-card ly-kb-side">
        <el-button class="ly-kb-new" :icon="Plus" @click="newConv">新对话</el-button>
        <div class="ly-kb-convs">
          <div
            v-for="c in convs"
            :key="c.id"
            class="ly-kb-conv"
            :class="{ 'is-active': c.id === activeConv }"
            @click="openConv(c.id)"
          >
            <span class="ly-kb-conv-title">{{ c.title || '未命名会话' }}</span>
            <el-icon class="ly-kb-conv-del" @click.stop="removeConv(c)"><Delete /></el-icon>
          </div>
          <p v-if="!convs.length" class="ly-muted ly-kb-empty-side">还没有历史会话</p>
        </div>
      </aside>

      <!-- 对话区 -->
      <section class="ly-card ly-kb-main">
        <div ref="bodyRef" v-loading="loadingHistory" class="ly-kb-body">
          <div v-if="!turns.length" class="ly-kb-welcome">
            <el-icon :size="44" class="ly-kb-welcome-icon"><ChatLineRound /></el-icon>
            <h3>问点什么？</h3>
            <p class="ly-muted">我会在你有权访问的文档里找答案，并给出出处。</p>
            <div class="ly-kb-examples">
              <el-tag
                v-for="e in examples"
                :key="e"
                class="ly-kb-example"
                @click="((question = e), send())"
              >
                {{ e }}
              </el-tag>
            </div>
          </div>

          <div v-for="(t, i) in turns" :key="i" class="ly-kb-turn" :class="`is-${t.role}`">
            <div class="ly-kb-bubble">
              <template v-if="t.status">
                <span class="ly-kb-status">
                  <el-icon class="is-spin"><Promotion /></el-icon>
                  {{ t.status }}
                </span>
              </template>
              <!--
                模型的回答是 Markdown。这里过的是 marked 解析 + DOMPurify 消毒，
                见 utils/markdown.ts——内容来自员工上传的文档，属于不可信输入，
                直接 v-html 会变成一个打到全公司的 XSS。
                提问那一侧仍然按纯文本显示，用户自己打的字没必要当富文本解析。
              -->
              <div
                v-if="t.content && t.role === 'assistant'"
                class="ly-kb-text ly-md"
                v-html="renderMarkdown(t.content)"
              ></div>
              <span v-else-if="t.content" class="ly-kb-text">{{ t.content }}</span>
              <span v-if="t.streaming && t.content" class="ly-kb-caret" />

              <div v-if="t.citations.length" class="ly-kb-cites">
                <div class="ly-kb-cites-head">参考来源</div>
                <div
                  v-for="(c, ci) in t.citations"
                  :key="c.chunk_id"
                  class="ly-kb-cite"
                  @click="openFile(c)"
                >
                  <span class="ly-kb-cite-no">[{{ ci + 1 }}]</span>
                  <el-icon><Document /></el-icon>
                  <span class="ly-kb-cite-name">{{ c.name }}</span>
                  <span v-if="whereOf(c)" class="ly-muted ly-kb-cite-path">{{ whereOf(c) }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="ly-kb-input">
          <el-input
            v-model="question"
            type="textarea"
            :rows="2"
            resize="none"
            placeholder="问一个问题，Enter 发送，Shift + Enter 换行"
            :disabled="sending"
            @keydown="onKeydown"
          />
          <div class="ly-kb-actions">
            <span class="ly-hint">回答由大模型生成，请以原文为准。</span>
            <el-button v-if="sending" @click="stop">停止</el-button>
            <el-button v-else type="primary" :icon="Promotion" :disabled="!canSend" @click="send">
              发送
            </el-button>
          </div>
        </div>
      </section>
    </div>

    <div v-else class="ly-card ly-card-pad" v-loading="true" style="height: 200px" />
  </div>
</template>

<style scoped>
.ly-assistant {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.ly-kb {
  display: grid;
  grid-template-columns: 230px 1fr;
  gap: 16px;
  flex: 1;
  min-height: 0;
}
.ly-kb-side {
  display: flex;
  flex-direction: column;
  padding: 12px;
  min-height: 0;
}
.ly-kb-new {
  width: 100%;
  margin-bottom: 10px;
}
.ly-kb-convs {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}
.ly-kb-conv {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 10px;
  border-radius: 8px;
  cursor: pointer;
  font-size: 13px;
  color: var(--ly-text-secondary);
}
.ly-kb-conv:hover {
  background: var(--ly-surface-sunken);
}
.ly-kb-conv.is-active {
  background: var(--ly-primary-soft);
  color: var(--ly-primary);
}
.ly-kb-conv-title {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ly-kb-conv-del {
  opacity: 0;
  flex-shrink: 0;
}
.ly-kb-conv:hover .ly-kb-conv-del {
  opacity: 0.6;
}
.ly-kb-conv-del:hover {
  opacity: 1;
  color: var(--ly-danger);
}
.ly-kb-empty-side {
  font-size: 12px;
  text-align: center;
  padding: 20px 0;
}

.ly-kb-main {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.ly-kb-body {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
  min-height: 0;
}
.ly-kb-welcome {
  text-align: center;
  padding: 48px 20px;
}
.ly-kb-welcome-icon {
  color: var(--ly-primary);
  opacity: 0.5;
}
.ly-kb-welcome h3 {
  margin: 12px 0 6px;
  font-size: 17px;
}
.ly-kb-examples {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  justify-content: center;
  margin-top: 18px;
}
.ly-kb-example {
  cursor: pointer;
}

.ly-kb-turn {
  display: flex;
  margin-bottom: 16px;
}
.ly-kb-turn.is-user {
  justify-content: flex-end;
}
.ly-kb-bubble {
  max-width: 76%;
  padding: 10px 14px;
  border-radius: 12px;
  background: var(--ly-surface-sunken);
  font-size: 14px;
  line-height: 1.8;
}
.is-user .ly-kb-bubble {
  background: var(--ly-primary);
  color: #fff;
}
.ly-kb-text {
  white-space: pre-wrap;
  word-break: break-word;
}

/*
 * Markdown 渲染出来的排版。
 *
 * 注意 .ly-md 要把 pre-wrap 关掉：Markdown 已经生成了 <p> <li> 这些块级元素，
 * 再叠一层 pre-wrap 的话，源文本里的换行会变成多余的空行，整段被撑开。
 *
 * 这些样式加了 :deep()——内容是 v-html 塞进去的，不带组件的 scoped 属性，
 * 不穿透的话一条都命中不了。
 */
.ly-md {
  white-space: normal;
  line-height: 1.75;
}
.ly-md :deep(p) {
  margin: 0 0 8px;
}
.ly-md :deep(p:last-child) {
  margin-bottom: 0;
}
.ly-md :deep(strong) {
  font-weight: 600;
}
.ly-md :deep(ul),
.ly-md :deep(ol) {
  margin: 6px 0 10px;
  padding-left: 22px;
}
.ly-md :deep(li) {
  margin: 3px 0;
}
.ly-md :deep(li > p) {
  margin: 0;
}
.ly-md :deep(h1),
.ly-md :deep(h2),
.ly-md :deep(h3),
.ly-md :deep(h4) {
  margin: 14px 0 8px;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.5;
}
.ly-md :deep(h1:first-child),
.ly-md :deep(h2:first-child),
.ly-md :deep(h3:first-child) {
  margin-top: 0;
}
.ly-md :deep(code) {
  padding: 1px 5px;
  border-radius: 4px;
  background: var(--ly-surface-sunken);
  font-family: var(--ly-font-mono);
  font-size: 0.92em;
}
.ly-md :deep(pre) {
  margin: 8px 0;
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--ly-surface-sunken);
  overflow-x: auto;
}
.ly-md :deep(pre code) {
  padding: 0;
  background: none;
}
.ly-md :deep(blockquote) {
  margin: 8px 0;
  padding: 2px 0 2px 12px;
  border-left: 3px solid var(--ly-border);
  color: var(--ly-text-secondary);
}
/* 规格参数这类回答经常是表格，得能横向滚动而不是把气泡撑破 */
.ly-md :deep(table) {
  display: block;
  width: max-content;
  max-width: 100%;
  overflow-x: auto;
  margin: 8px 0;
  border-collapse: collapse;
  font-size: 13px;
}
.ly-md :deep(th),
.ly-md :deep(td) {
  padding: 6px 10px;
  border: 1px solid var(--ly-border);
  text-align: left;
}
.ly-md :deep(th) {
  background: var(--ly-surface-sunken);
  font-weight: 600;
}
.ly-md :deep(hr) {
  margin: 12px 0;
  border: none;
  border-top: 1px solid var(--ly-border);
}
.ly-md :deep(a) {
  color: var(--ly-primary);
  text-decoration: none;
}
.ly-md :deep(a:hover) {
  text-decoration: underline;
}
.ly-kb-status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--ly-text-tertiary);
  font-size: 13px;
}
.is-spin {
  animation: ly-spin 1.2s linear infinite;
}
@keyframes ly-spin {
  to {
    transform: rotate(360deg);
  }
}
/* 生成中的光标，让人一眼看出还在写 */
.ly-kb-caret {
  display: inline-block;
  width: 2px;
  height: 14px;
  margin-left: 2px;
  background: currentColor;
  vertical-align: text-bottom;
  animation: ly-blink 1s step-end infinite;
}
@keyframes ly-blink {
  50% {
    opacity: 0;
  }
}

.ly-kb-cites {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--ly-border);
}
.ly-kb-cites-head {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  margin-bottom: 6px;
}
.ly-kb-cite {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 0;
  font-size: 12.5px;
  cursor: pointer;
}
.ly-kb-cite:hover .ly-kb-cite-name {
  color: var(--ly-primary);
  text-decoration: underline;
}
.ly-kb-cite-no {
  color: var(--ly-primary);
  font-weight: 600;
}
.ly-kb-cite-name {
  font-weight: 500;
}
.ly-kb-cite-path {
  font-size: 11.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ly-kb-input {
  border-top: 1px solid var(--ly-border);
  padding: 12px 16px 14px;
}
.ly-kb-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  margin-top: 8px;
}
.ly-kb-actions .ly-hint {
  margin-right: auto;
}

.ly-kb-off {
  text-align: center;
  padding: 56px 20px;
}
.ly-kb-off h3 {
  margin: 12px 0 6px;
  font-size: 16px;
}
.ly-kb-off p {
  margin: 0 0 18px;
}

@media (max-width: 900px) {
  .ly-kb {
    grid-template-columns: 1fr;
  }
  /* 手机上会话列表折到上面，且不抢走对话区的高度 */
  .ly-kb-side {
    max-height: 160px;
  }
  .ly-kb-bubble {
    max-width: 92%;
  }
}
</style>
