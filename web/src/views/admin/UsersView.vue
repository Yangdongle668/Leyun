<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '@/api'
import type { Department, User } from '@/api/types'
import { useUserStore } from '@/stores/user'
import { bytesToGB, formatTime, gbToBytes, humanSize } from '@/utils/format'

const store = useUserStore()

const loading = ref(false)
const list = ref<User[]>([])
const total = ref(0)
const departments = ref<Department[]>([])

const query = reactive({
  keyword: '',
  dept_id: 0,
  role: '',
  status: '',
  page: 1,
  page_size: 20,
})

const dialog = reactive({
  visible: false,
  mode: 'create' as 'create' | 'edit',
  id: 0,
  submitting: false,
})

const form = reactive({
  username: '',
  password: '',
  nickname: '',
  email: '',
  phone: '',
  job_title: '',
  dept_id: 0,
  role: 'member',
  quotaGB: 10,
  remark: '',
  must_change_password: true,
  status: 'active',
})

const deptOptions = computed(() =>
  departments.value.map((d) => ({
    value: d.id,
    label: `${'　'.repeat(Math.max(0, d.depth))}${d.depth > 0 ? '└ ' : ''}${d.name}`,
  })),
)

async function load() {
  loading.value = true
  try {
    const res = await api.adminUsers({
      keyword: query.keyword || undefined,
      dept_id: query.dept_id || undefined,
      include_sub: true,
      role: query.role || undefined,
      status: query.status || undefined,
      page: query.page,
      page_size: query.page_size,
    })
    list.value = res.list
    total.value = res.total
  } finally {
    loading.value = false
  }
}

async function loadDepartments() {
  departments.value = await api.departments()
}

onMounted(async () => {
  await Promise.all([load(), loadDepartments()])
})

function search() {
  query.page = 1
  load()
}

function resetForm() {
  Object.assign(form, {
    username: '',
    password: '',
    nickname: '',
    email: '',
    phone: '',
    job_title: '',
    dept_id: departments.value[0]?.id ?? 0,
    role: 'member',
    quotaGB: 10,
    remark: '',
    must_change_password: true,
    status: 'active',
  })
}

function openCreate() {
  resetForm()
  dialog.mode = 'create'
  dialog.id = 0
  dialog.visible = true
}

function openEdit(row: User) {
  Object.assign(form, {
    username: row.username,
    password: '',
    nickname: row.nickname,
    email: row.email,
    phone: row.phone,
    job_title: row.job_title,
    dept_id: row.dept_id,
    role: row.role,
    quotaGB: bytesToGB(row.quota_bytes),
    remark: row.remark,
    must_change_password: row.must_change_password,
    status: row.status,
  })
  dialog.mode = 'edit'
  dialog.id = row.id
  dialog.visible = true
}

/** 生成一个好念、好抄的初始口令。 */
function randomPassword() {
  const chars = 'abcdefghjkmnpqrstuvwxyz23456789'
  let out = ''
  for (let i = 0; i < 10; i += 1) out += chars[Math.floor(Math.random() * chars.length)]
  form.password = out
}

async function submit() {
  if (!form.dept_id) {
    ElMessage.warning('请选择所属部门')
    return
  }
  dialog.submitting = true
  try {
    if (dialog.mode === 'create') {
      if (!form.username.trim() || !form.password) {
        ElMessage.warning('请填写用户名与初始口令')
        return
      }
      await api.createUser({
        username: form.username.trim(),
        password: form.password,
        nickname: form.nickname.trim() || undefined,
        email: form.email.trim() || undefined,
        phone: form.phone.trim() || undefined,
        job_title: form.job_title.trim() || undefined,
        dept_id: form.dept_id,
        role: form.role,
        quota: gbToBytes(form.quotaGB),
        remark: form.remark || undefined,
        must_change_password: form.must_change_password,
      })
      ElMessage.success(`账号「${form.username}」已开通`)
    } else {
      await api.updateUser(dialog.id, {
        nickname: form.nickname.trim(),
        email: form.email.trim(),
        phone: form.phone.trim(),
        job_title: form.job_title.trim(),
        dept_id: form.dept_id,
        role: form.role,
        quota: gbToBytes(form.quotaGB),
        remark: form.remark,
        status: form.status,
      })
      ElMessage.success('账号已更新')
    }
    dialog.visible = false
    await load()
  } finally {
    dialog.submitting = false
  }
}

async function resetPassword(row: User) {
  const { value } = await ElMessageBox.prompt(
    `为「${row.nickname || row.username}」设置新的初始口令，用户下次登录后需自行修改。`,
    '重置口令',
    {
      confirmButtonText: '重置',
      cancelButtonText: '取消',
      inputPlaceholder: '新口令',
      inputPattern: /^.{5,}$/,
      inputErrorMessage: '口令至少 5 位',
    },
  )
  await api.resetPassword(row.id, value, true)
  ElMessage.success('口令已重置')
}

async function toggleStatus(row: User) {
  const next = row.status === 'active' ? 'disabled' : 'active'
  const label = next === 'disabled' ? '停用' : '启用'
  await ElMessageBox.confirm(
    next === 'disabled'
      ? `停用后「${row.nickname || row.username}」将无法登录，已有文件保留不变。`
      : `确定重新启用「${row.nickname || row.username}」吗？`,
    `${label}账号`,
    { type: 'warning', confirmButtonText: label, cancelButtonText: '取消' },
  )
  await api.updateUser(row.id, { status: next })
  ElMessage.success(`已${label}`)
  await load()
}

async function removeUser(row: User) {
  await ElMessageBox.confirm(
    `将删除账号「${row.nickname || row.username}」。若其个人空间中仍有文件，系统会拒绝删除。`,
    '删除账号',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' },
  )
  await api.deleteUser(row.id)
  ElMessage.success('账号已删除')
  await load()
}
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">账号管理</h1>
        <p class="ly-page-desc">
          系统不开放自助注册，所有账号只能在这里由超级管理员开通。
        </p>
      </div>
      <el-button v-if="store.isSuperAdmin" type="primary" :icon="'Plus'" @click="openCreate">
        开通账号
      </el-button>
    </div>

    <div class="ly-card">
      <div class="ly-filters">
        <el-input
          v-model="query.keyword"
          placeholder="搜索姓名 / 用户名 / 邮箱"
          :prefix-icon="'Search'"
          clearable
          style="width: 240px"
          @keyup.enter="search"
          @clear="search"
        />
        <el-select v-model="query.dept_id" placeholder="全部部门" clearable style="width: 200px" @change="search">
          <el-option :value="0" label="全部部门" />
          <el-option v-for="o in deptOptions" :key="o.value" :label="o.label" :value="o.value" />
        </el-select>
        <el-select v-model="query.role" placeholder="全部角色" clearable style="width: 150px" @change="search">
          <el-option value="" label="全部角色" />
          <el-option value="super_admin" label="超级管理员" />
          <el-option value="dept_admin" label="部门管理员" />
          <el-option value="member" label="普通成员" />
        </el-select>
        <el-select v-model="query.status" placeholder="全部状态" clearable style="width: 130px" @change="search">
          <el-option value="" label="全部状态" />
          <el-option value="active" label="正常" />
          <el-option value="disabled" label="已停用" />
        </el-select>
      </div>

      <el-table v-loading="loading" :data="list" row-key="id">
        <el-table-column label="成员" min-width="190">
          <template #default="{ row }">
            <div class="ly-user-cell">
              <span class="ly-user-avatar">{{ (row.nickname || row.username).slice(0, 1) }}</span>
              <div>
                <div class="ly-user-name">
                  {{ row.nickname || row.username }}
                  <span v-if="row.must_change_password" class="ly-tag ly-tag--warning">初始口令</span>
                </div>
                <div class="ly-muted ly-mono">{{ row.username }}</div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="部门" min-width="180">
          <template #default="{ row }">
            <span>{{ row.dept_path || row.dept_name || '—' }}</span>
            <div v-if="row.job_title" class="ly-muted" style="font-size: 12px">{{ row.job_title }}</div>
          </template>
        </el-table-column>
        <el-table-column label="角色" width="120">
          <template #default="{ row }">
            <span
              class="ly-tag"
              :class="{
                'ly-tag--danger': row.role === 'super_admin',
                'ly-tag--primary': row.role === 'dept_admin',
              }"
            >
              {{ row.role_label }}
            </span>
          </template>
        </el-table-column>
        <el-table-column label="容量" width="140">
          <template #default="{ row }">
            <span>{{ humanSize(row.used_bytes) }}</span>
            <span class="ly-muted"> / {{ row.quota_display }}</span>
          </template>
        </el-table-column>
        <el-table-column label="上次登录" width="150">
          <template #default="{ row }">
            <span class="ly-muted">{{ formatTime(row.last_login_at, false) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="88">
          <template #default="{ row }">
            <span class="ly-tag" :class="row.status === 'active' ? 'ly-tag--success' : 'ly-tag--danger'">
              {{ row.status_label }}
            </span>
          </template>
        </el-table-column>
        <el-table-column v-if="store.isSuperAdmin" label="操作" width="210" align="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
            <el-button link type="primary" size="small" @click="resetPassword(row)">重置口令</el-button>
            <el-button link size="small" @click="toggleStatus(row)">
              {{ row.status === 'active' ? '停用' : '启用' }}
            </el-button>
            <el-button link type="danger" size="small" @click="removeUser(row)">删除</el-button>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">没有符合条件的账号</div>
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

    <!-- 开通 / 编辑 -->
    <el-dialog
      v-model="dialog.visible"
      :title="dialog.mode === 'create' ? '开通账号' : '编辑账号'"
      width="560px"
      destroy-on-close
    >
      <el-form label-width="90px" label-position="left">
        <el-form-item label="用户名" required>
          <el-input v-model="form.username" :disabled="dialog.mode === 'edit'" placeholder="登录名，创建后不可修改" />
        </el-form-item>
        <el-form-item v-if="dialog.mode === 'create'" label="初始口令" required>
          <div class="ly-inline">
            <el-input v-model="form.password" placeholder="至少 5 位" />
            <el-button @click="randomPassword">随机生成</el-button>
          </div>
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="form.nickname" placeholder="留空则与用户名相同" />
        </el-form-item>
        <el-form-item label="所属部门" required>
          <el-select v-model="form.dept_id" filterable style="width: 100%">
            <el-option v-for="o in deptOptions" :key="o.value" :label="o.label" :value="o.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role" style="width: 100%">
            <el-option value="member" label="普通成员" />
            <el-option value="dept_admin" label="部门管理员（管理本部门及下级空间，不能开通账号）" />
            <el-option value="super_admin" label="超级管理员（可开通账号、拥有全部权限）" />
          </el-select>
        </el-form-item>
        <el-form-item label="个人配额">
          <el-input-number v-model="form.quotaGB" :min="0" :max="102400" style="width: 160px" />
          <span class="ly-muted" style="margin-left: 10px">GB，0 表示不限</span>
        </el-form-item>
        <el-form-item label="职位">
          <el-input v-model="form.job_title" placeholder="选填" />
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input v-model="form.email" placeholder="选填" />
        </el-form-item>
        <el-form-item label="手机">
          <el-input v-model="form.phone" placeholder="选填" />
        </el-form-item>
        <el-form-item v-if="dialog.mode === 'edit'" label="状态">
          <el-radio-group v-model="form.status">
            <el-radio value="active">正常</el-radio>
            <el-radio value="disabled">停用</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="dialog.mode === 'create'" label="">
          <el-checkbox v-model="form.must_change_password">要求首次登录后修改口令</el-checkbox>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.remark" type="textarea" :rows="2" placeholder="选填" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.submitting" @click="submit">
          {{ dialog.mode === 'create' ? '开通' : '保存' }}
        </el-button>
      </template>
    </el-dialog>
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
.ly-user-cell {
  display: flex;
  align-items: center;
  gap: 10px;
}
.ly-user-avatar {
  width: 32px;
  height: 32px;
  border-radius: 10px;
  background: var(--ly-primary-soft);
  color: var(--ly-primary);
  font-size: 13px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.ly-user-name {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  line-height: 1.3;
}
.ly-inline {
  display: flex;
  gap: 8px;
  width: 100%;
}
.ly-pager {
  display: flex;
  justify-content: flex-end;
  padding: 14px 16px;
  border-top: 1px solid var(--ly-border);
}
</style>
