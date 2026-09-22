<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '@/api'
import type { AccessRule, Department, PermCode, PrincipalType, User } from '@/api/types'
import { permLabel } from '@/utils/format'

const props = defineProps<{
  modelValue: boolean
  spaceId: number
  nodeId: number
  title: string
}>()
const emit = defineEmits<{ 'update:modelValue': [boolean] }>()

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})

const loading = ref(false)
const direct = ref<AccessRule[]>([])
const inherited = ref<AccessRule[]>([])
const inherit = ref(true)
const canToggleInherit = ref(false)
const togglingInherit = ref(false)

const ALL_PERMS: PermCode[] = ['view', 'download', 'upload', 'edit', 'delete', 'share', 'manage']

/** 常用权限套餐，比逐个勾选快得多。 */
const presets: Array<{ key: string; label: string; desc: string; perms: PermCode[] }> = [
  { key: 'read', label: '只读', desc: '可浏览、可下载', perms: ['view', 'download'] },
  { key: 'view', label: '仅预览', desc: '可看不可下载', perms: ['view'] },
  {
    key: 'collab',
    label: '协作',
    desc: '可上传、编辑、删除、分享',
    perms: ['view', 'download', 'upload', 'edit', 'delete', 'share'],
  },
  { key: 'full', label: '完全控制', desc: '含授权管理', perms: ALL_PERMS },
]

const form = reactive({
  principalType: 'dept' as PrincipalType,
  // 初值留空而不是 0，否则下拉框会把 0 当成已选项显示出来，看不到占位提示。
  userId: undefined as number | undefined,
  deptId: undefined as number | undefined,
  role: 'member',
  allow: ['view', 'download'] as PermCode[],
  deny: [] as PermCode[],
  includeSubDept: true,
  inheritable: true,
  expireDays: 0,
  remark: '',
})

const userOptions = ref<User[]>([])
const userLoading = ref(false)
const departments = ref<Department[]>([])
const submitting = ref(false)
const showDeny = ref(false)

const deptOptions = computed(() =>
  departments.value.map((d) => ({
    value: d.id,
    label: `${'　'.repeat(Math.max(0, d.depth))}${d.depth > 0 ? '└ ' : ''}${d.name}`,
  })),
)

async function load() {
  loading.value = true
  try {
    const res = await api.listACL(props.spaceId, props.nodeId)
    direct.value = res.direct
    inherited.value = res.inherited
    inherit.value = res.inherit
    canToggleInherit.value = res.can_toggle_inherit
  } finally {
    loading.value = false
  }
}

/**
 * 切断继承是"这个目录只给某几个人"的正确做法。
 *
 * 用拒绝规则去挡部门是不行的——拒绝优先于一切允许，会把你想放行的那个人
 * 一起挡在外面。所以这里要把后果讲清楚再让人点确认。
 */
async function toggleInherit(next: boolean) {
  const title = next ? '恢复继承上层权限' : '切断继承上层权限'
  const message = next
    ? '恢复后，空间根与上级目录的授权会重新对本目录生效。'
    : '切断后，本目录及其子目录不再接收上层传下来的任何授权，只认下面「本级授权」里的规则。' +
      '如果此处还没有任何授权，除超级管理员外将没有人能进入。'
  try {
    await ElMessageBox.confirm(message, title, {
      type: 'warning',
      confirmButtonText: next ? '恢复继承' : '切断继承',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  togglingInherit.value = true
  try {
    await api.setInheritance(props.spaceId, props.nodeId, next)
    ElMessage.success(next ? '已恢复继承' : '已切断继承')
    await load()
  } finally {
    togglingInherit.value = false
  }
}

async function loadDepartments() {
  if (departments.value.length) return
  departments.value = await api.departments()
}

async function searchUsers(keyword: string) {
  userLoading.value = true
  try {
    userOptions.value = await api.searchUsers(keyword, 30)
  } finally {
    userLoading.value = false
  }
}

watch(visible, async (open) => {
  if (!open) return
  await Promise.all([load(), loadDepartments(), searchUsers('')])
})

function applyPreset(perms: PermCode[]) {
  form.allow = [...perms]
}

const activePreset = computed(() => {
  const sorted = [...form.allow].sort().join(',')
  return presets.find((p) => [...p.perms].sort().join(',') === sorted)?.key ?? ''
})

async function submit() {
  if (form.principalType === 'user' && !form.userId) {
    ElMessage.warning('请选择要授权的成员')
    return
  }
  if (form.principalType === 'dept' && !form.deptId) {
    ElMessage.warning('请选择要授权的部门')
    return
  }
  if (!form.allow.length && !form.deny.length) {
    ElMessage.warning('请至少选择一项权限')
    return
  }
  submitting.value = true
  try {
    await api.grant({
      space_id: props.spaceId,
      node_id: props.nodeId,
      principal_type: form.principalType,
      principal_id:
        form.principalType === 'user'
          ? (form.userId ?? 0)
          : form.principalType === 'dept'
            ? (form.deptId ?? 0)
            : 0,
      principal_role: form.principalType === 'role' ? form.role : undefined,
      allow: form.allow,
      deny: form.deny,
      include_sub_dept: form.includeSubDept,
      inheritable: form.inheritable,
      expire_days: form.expireDays || undefined,
      remark: form.remark || undefined,
    })
    ElMessage.success('授权已保存')
    form.remark = ''
    await load()
  } finally {
    submitting.value = false
  }
}

async function revoke(rule: AccessRule) {
  await ElMessageBox.confirm(
    `确定撤销「${rule.principal_name}」在此处的权限吗？`,
    '撤销授权',
    { type: 'warning', confirmButtonText: '撤销', cancelButtonText: '取消' },
  )
  await api.revokeACL(rule.id)
  ElMessage.success('已撤销')
  await load()
}

function principalTypeLabel(t: PrincipalType) {
  return { user: '成员', dept: '部门', role: '角色', everyone: '全员' }[t] ?? t
}
</script>

<template>
  <el-dialog v-model="visible" width="860px" top="6vh" destroy-on-close>
    <template #header>
      <div class="ly-dlg-head">
        <span>权限设置</span>
        <span class="ly-dlg-sub">{{ title }}</span>
      </div>
    </template>

    <div class="ly-perm" v-loading="loading">
      <!-- 新增授权 -->
      <div class="ly-perm-form">
        <div class="ly-perm-row">
          <label>授权对象</label>
          <div class="ly-perm-ctl">
            <el-radio-group v-model="form.principalType" size="small">
              <el-radio-button value="dept">部门</el-radio-button>
              <el-radio-button value="user">成员</el-radio-button>
              <el-radio-button value="role">角色</el-radio-button>
              <el-radio-button value="everyone">全员</el-radio-button>
            </el-radio-group>

            <el-select
              v-if="form.principalType === 'dept'"
              v-model="form.deptId"
              placeholder="选择部门"
              filterable
              style="width: 240px"
            >
              <el-option
                v-for="opt in deptOptions"
                :key="opt.value"
                :label="opt.label"
                :value="opt.value"
              />
            </el-select>

            <el-select
              v-else-if="form.principalType === 'user'"
              v-model="form.userId"
              placeholder="搜索姓名或用户名"
              filterable
              remote
              :remote-method="searchUsers"
              :loading="userLoading"
              style="width: 240px"
            >
              <el-option
                v-for="u in userOptions"
                :key="u.id"
                :label="`${u.nickname || u.username}（${u.username}）`"
                :value="u.id"
              >
                <span>{{ u.nickname || u.username }}</span>
                <span class="ly-muted" style="margin-left: 8px; font-size: 12px">{{ u.dept_name }}</span>
              </el-option>
            </el-select>

            <el-select v-else-if="form.principalType === 'role'" v-model="form.role" style="width: 180px">
              <el-option label="超级管理员" value="super_admin" />
              <el-option label="部门管理员" value="dept_admin" />
              <el-option label="普通成员" value="member" />
            </el-select>

            <span v-else class="ly-muted">所有已登录的企业成员</span>
          </div>
        </div>

        <div class="ly-perm-row">
          <label>权限</label>
          <div class="ly-perm-ctl ly-perm-presets">
            <button
              v-for="p in presets"
              :key="p.key"
              class="ly-preset"
              :class="{ 'is-active': activePreset === p.key }"
              type="button"
              @click="applyPreset(p.perms)"
            >
              <strong>{{ p.label }}</strong>
              <span>{{ p.desc }}</span>
            </button>
          </div>
        </div>

        <div class="ly-perm-row">
          <label></label>
          <div class="ly-perm-ctl">
            <el-checkbox-group v-model="form.allow" size="small">
              <el-checkbox v-for="code in ALL_PERMS" :key="code" :value="code" :label="permLabel(code)" />
            </el-checkbox-group>
          </div>
        </div>

        <div class="ly-perm-row">
          <label>生效范围</label>
          <div class="ly-perm-ctl">
            <el-checkbox v-model="form.inheritable" size="small">向子目录继承</el-checkbox>
            <el-checkbox
              v-if="form.principalType === 'dept'"
              v-model="form.includeSubDept"
              size="small"
            >
              包含下级部门成员
            </el-checkbox>
            <span class="ly-perm-inline">
              有效期
              <el-input-number v-model="form.expireDays" :min="0" :max="3650" size="small" style="width: 116px" />
              天（0 为长期）
            </span>
          </div>
        </div>

        <div class="ly-perm-row">
          <label></label>
          <div class="ly-perm-ctl">
            <el-link type="info" :underline="false" @click="showDeny = !showDeny">
              {{ showDeny ? '收起' : '高级：设置显式拒绝' }}
            </el-link>
          </div>
        </div>

        <div v-if="showDeny" class="ly-perm-row">
          <label>显式拒绝</label>
          <div class="ly-perm-ctl">
            <el-checkbox-group v-model="form.deny" size="small">
              <el-checkbox v-for="code in ALL_PERMS" :key="code" :value="code" :label="permLabel(code)" />
            </el-checkbox-group>
            <div class="ly-perm-hint">
              拒绝优先于一切允许。用于「整个部门可读，唯独某人除外」这类场景。
            </div>
          </div>
        </div>

        <div class="ly-perm-row">
          <label></label>
          <div class="ly-perm-ctl">
            <el-input
              v-model="form.remark"
              placeholder="备注（选填，例如：项目期间临时开放）"
              size="small"
              style="max-width: 330px"
            />
            <el-button type="primary" size="small" :loading="submitting" @click="submit">
              保存授权
            </el-button>
          </div>
        </div>
      </div>

      <!-- 继承开关 -->
      <div v-if="canToggleInherit" class="ly-perm-inherit" :class="{ 'is-cut': !inherit }">
        <el-icon :size="16">
          <Connection v-if="inherit" />
          <Scissor v-else />
        </el-icon>
        <div class="ly-perm-inherit-text">
          <strong>{{ inherit ? '继承上层权限' : '已切断继承' }}</strong>
          <span>
            {{
              inherit
                ? '空间根与上级目录的授权对本目录生效。'
                : '本目录只认下面的「本级授权」，上层授权一概不生效。'
            }}
          </span>
        </div>
        <el-button size="small" :loading="togglingInherit" @click="toggleInherit(!inherit)">
          {{ inherit ? '切断继承' : '恢复继承' }}
        </el-button>
      </div>

      <!-- 已有授权 -->
      <div class="ly-perm-list">
        <div class="ly-perm-list-title">
          本级授权
          <span class="ly-muted">（直接挂在当前位置）</span>
        </div>
        <el-table :data="direct" size="small" empty-text="尚未设置任何授权">
          <el-table-column label="对象" min-width="190">
            <template #default="{ row }">
              <span class="ly-tag">{{ principalTypeLabel(row.principal_type) }}</span>
              <span style="margin-left: 8px">{{ row.principal_name }}</span>
            </template>
          </el-table-column>
          <el-table-column label="允许" min-width="220">
            <template #default="{ row }">
              <span v-if="!row.allow_codes.length" class="ly-muted">—</span>
              <span v-for="c in row.allow_codes" :key="c" class="ly-tag ly-tag--primary ly-perm-chip">
                {{ permLabel(c) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="拒绝" min-width="140">
            <template #default="{ row }">
              <span v-if="!row.deny_codes.length" class="ly-muted">—</span>
              <span v-for="c in row.deny_codes" :key="c" class="ly-tag ly-tag--danger ly-perm-chip">
                {{ permLabel(c) }}
              </span>
            </template>
          </el-table-column>
          <el-table-column label="继承" width="72" align="center">
            <template #default="{ row }">
              <el-icon v-if="row.inheritable" color="#12b76a"><Select /></el-icon>
              <span v-else class="ly-muted">否</span>
            </template>
          </el-table-column>
          <el-table-column width="70" align="right">
            <template #default="{ row }">
              <el-button link type="danger" size="small" @click="revoke(row)">撤销</el-button>
            </template>
          </el-table-column>
        </el-table>

        <template v-if="inherited.length">
          <div class="ly-perm-list-title" style="margin-top: 18px">
            继承的授权
            <span class="ly-muted">（来自上级目录或空间根，需到来源处修改）</span>
          </div>
          <!-- 切断继承时后端不会返回这些规则，这里出现即说明它们确实生效 -->
          <el-table :data="inherited" size="small">
            <el-table-column label="对象" min-width="190">
              <template #default="{ row }">
                <span class="ly-tag">{{ principalTypeLabel(row.principal_type) }}</span>
                <span style="margin-left: 8px">{{ row.principal_name }}</span>
              </template>
            </el-table-column>
            <el-table-column label="允许" min-width="220">
              <template #default="{ row }">
                <span v-for="c in row.allow_codes" :key="c" class="ly-tag ly-perm-chip">
                  {{ permLabel(c) }}
                </span>
              </template>
            </el-table-column>
            <el-table-column label="拒绝" min-width="140">
              <template #default="{ row }">
                <span v-if="!row.deny_codes.length" class="ly-muted">—</span>
                <span v-for="c in row.deny_codes" :key="c" class="ly-tag ly-tag--danger ly-perm-chip">
                  {{ permLabel(c) }}
                </span>
              </template>
            </el-table-column>
          </el-table>
        </template>
      </div>
    </div>

    <template #footer>
      <el-button @click="visible = false">关闭</el-button>
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
  max-width: 480px;
}

.ly-perm-form {
  background: var(--ly-surface-sunken);
  border: 1px solid var(--ly-border);
  border-radius: var(--ly-radius);
  padding: 16px 18px 6px;
  margin-bottom: 20px;
}
.ly-perm-row {
  display: flex;
  gap: 14px;
  margin-bottom: 12px;
}
.ly-perm-row > label {
  width: 68px;
  flex-shrink: 0;
  font-size: 13px;
  color: var(--ly-text-secondary);
  line-height: 24px;
  text-align: right;
}
.ly-perm-ctl {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.ly-perm-inline {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--ly-text-secondary);
}
.ly-perm-hint {
  width: 100%;
  font-size: 12px;
  color: var(--ly-text-tertiary);
}

.ly-perm-presets {
  gap: 8px;
}
.ly-preset {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  padding: 7px 12px;
  border: 1px solid var(--ly-border-strong);
  border-radius: 8px;
  background: var(--ly-surface);
  cursor: pointer;
  font-family: inherit;
  text-align: left;
  transition: all 0.15s;
}
.ly-preset strong {
  font-size: 13px;
  font-weight: 600;
  color: var(--ly-text);
}
.ly-preset span {
  font-size: 11px;
  color: var(--ly-text-tertiary);
}
.ly-preset:hover {
  border-color: var(--ly-primary-border);
}
.ly-preset.is-active {
  border-color: var(--ly-primary);
  background: var(--ly-primary-soft);
}
.ly-preset.is-active strong {
  color: var(--ly-primary);
}

.ly-perm-inherit {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 16px;
  margin-bottom: 18px;
  border-radius: var(--ly-radius);
  background: var(--ly-primary-soft);
  border: 1px solid var(--ly-primary-border);
  color: var(--ly-primary);
}
.ly-perm-inherit.is-cut {
  background: #fff6e6;
  border-color: #fae3bb;
  color: #b5741a;
}
.ly-perm-inherit-text {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  line-height: 1.5;
}
.ly-perm-inherit-text strong {
  font-size: 13px;
  font-weight: 600;
}
.ly-perm-inherit-text span {
  font-size: 12px;
  color: var(--ly-text-secondary);
}

.ly-perm-list-title {
  font-size: 13px;
  font-weight: 600;
  margin-bottom: 8px;
  color: var(--ly-text);
}
.ly-perm-chip {
  margin-right: 4px;
}
</style>
