<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '@/api'
import type { DeptNode } from '@/api/types'
import { useUserStore } from '@/stores/user'
import { gbToBytes } from '@/utils/format'

const store = useUserStore()
const loading = ref(false)
const tree = ref<DeptNode[]>([])
const expanded = ref<number[]>([])

const dialog = reactive({
  visible: false,
  mode: 'create' as 'create' | 'edit',
  id: 0,
  submitting: false,
})
const form = reactive({
  parent_id: 0,
  name: '',
  code: '',
  sort: 0,
  remark: '',
  quotaGB: 100,
  enabled: true,
})

/** 平铺的部门列表，用于"上级部门"下拉。 */
const flat = computed(() => {
  const out: Array<{ id: number; label: string; depth: number }> = []
  const walk = (nodes: DeptNode[]) => {
    nodes.forEach((n) => {
      out.push({
        id: n.id,
        label: `${'　'.repeat(Math.max(0, n.depth))}${n.depth > 0 ? '└ ' : ''}${n.name}`,
        depth: n.depth,
      })
      if (n.children?.length) walk(n.children)
    })
  }
  walk(tree.value)
  return out
})

async function load() {
  loading.value = true
  try {
    tree.value = await api.deptTree()
    // 默认展开前两层，再深就折叠，免得一屏塞满。
    const ids: number[] = []
    const walk = (nodes: DeptNode[]) => {
      nodes.forEach((n) => {
        if (n.depth < 2) ids.push(n.id)
        if (n.children?.length) walk(n.children)
      })
    }
    walk(tree.value)
    expanded.value = ids
  } finally {
    loading.value = false
  }
}
onMounted(load)

function openCreate(parent?: DeptNode) {
  Object.assign(form, {
    parent_id: parent?.id ?? tree.value[0]?.id ?? 0,
    name: '',
    code: '',
    sort: 0,
    remark: '',
    quotaGB: 100,
    enabled: true,
  })
  dialog.mode = 'create'
  dialog.id = 0
  dialog.visible = true
}

function openEdit(node: DeptNode) {
  Object.assign(form, {
    parent_id: node.parent_id,
    name: node.name,
    code: node.code,
    sort: node.sort,
    remark: node.remark,
    quotaGB: 100,
    enabled: node.enabled,
  })
  dialog.mode = 'edit'
  dialog.id = node.id
  dialog.visible = true
}

async function submit() {
  if (!form.name.trim()) {
    ElMessage.warning('请输入部门名称')
    return
  }
  dialog.submitting = true
  try {
    if (dialog.mode === 'create') {
      await api.createDept({
        parent_id: form.parent_id,
        name: form.name.trim(),
        code: form.code.trim() || undefined,
        sort: form.sort,
        remark: form.remark || undefined,
        quota: gbToBytes(form.quotaGB),
      })
      ElMessage.success('部门已创建，并自动建立了同名部门空间')
    } else {
      await api.updateDept(dialog.id, {
        name: form.name.trim(),
        code: form.code.trim(),
        sort: form.sort,
        remark: form.remark,
        enabled: form.enabled,
        parent_id: form.parent_id,
      })
      ElMessage.success('部门已更新')
    }
    dialog.visible = false
    await Promise.all([load(), store.refreshSpaces()])
  } finally {
    dialog.submitting = false
  }
}

async function removeDept(node: DeptNode) {
  await ElMessageBox.confirm(
    `将删除部门「${node.name}」及其部门空间。若该部门下还有成员、下级部门或文件，系统会拒绝删除。`,
    '删除部门',
    { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' },
  )
  await api.deleteDept(node.id)
  ElMessage.success('部门已删除')
  await Promise.all([load(), store.refreshSpaces()])
}
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">部门管理</h1>
        <p class="ly-page-desc">
          部门树是权限的骨架：新建部门会自动创建同名部门空间，本部门成员默认可读写。
        </p>
      </div>
      <el-button v-if="store.isSuperAdmin" type="primary" :icon="'Plus'" @click="openCreate()">
        新建部门
      </el-button>
    </div>

    <div class="ly-card">
      <el-table
        v-loading="loading"
        :data="tree"
        row-key="id"
        :tree-props="{ children: 'children' }"
        :expand-row-keys="expanded"
        default-expand-all
      >
        <el-table-column label="部门" min-width="260">
          <template #default="{ row }">
            <div class="ly-dept-cell">
              <el-icon :color="row.depth === 0 ? '#1f5eff' : '#98a1b3'">
                <OfficeBuilding v-if="row.depth === 0" />
                <Connection v-else />
              </el-icon>
              <span class="ly-dept-name">{{ row.name }}</span>
              <span v-if="row.code" class="ly-tag ly-mono">{{ row.code }}</span>
              <span v-if="!row.enabled" class="ly-tag ly-tag--danger">已停用</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="成员" width="100">
          <template #default="{ row }">
            <!--
              原来这里只是一个数字，看得见人数却点不进去，
              想知道"这个部门都有谁、谁是部门管理员"只能自己去账号管理里翻。
              现在带着 dept_id 跳过去，落地就是筛好的。
            -->
            <router-link
              v-if="row.user_count > 0"
              :to="{ name: 'admin-users', query: { dept_id: row.id } }"
            >
              {{ row.user_count }} 人
            </router-link>
            <span v-else class="ly-muted">0 人</span>
          </template>
        </el-table-column>
        <el-table-column label="部门空间" width="120">
          <template #default="{ row }">
            <router-link
              v-if="row.space_id"
              :to="{ name: 'files', params: { spaceId: String(row.space_id) } }"
            >
              进入空间
            </router-link>
            <span v-else class="ly-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column label="备注" min-width="180">
          <template #default="{ row }">
            <span class="ly-muted">{{ row.remark || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column v-if="store.isSuperAdmin" label="操作" width="190" align="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" @click="openCreate(row)">添加下级</el-button>
            <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
            <el-button
              v-if="row.parent_id !== 0"
              link
              type="danger"
              size="small"
              @click="removeDept(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>

        <template #empty>
          <div class="ly-empty">还没有部门</div>
        </template>
      </el-table>
    </div>

    <el-dialog
      v-model="dialog.visible"
      :title="dialog.mode === 'create' ? '新建部门' : '编辑部门'"
      width="500px"
      destroy-on-close
    >
      <el-form label-width="86px" label-position="left">
        <el-form-item label="上级部门">
          <el-select v-model="form.parent_id" filterable style="width: 100%">
            <el-option :value="0" label="（作为顶级部门）" />
            <el-option
              v-for="d in flat"
              :key="d.id"
              :label="d.label"
              :value="d.id"
              :disabled="d.id === dialog.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="部门名称" required>
          <el-input v-model="form.name" maxlength="64" />
        </el-form-item>
        <el-form-item label="部门编码">
          <el-input v-model="form.code" placeholder="选填，例如 RD / MKT" />
        </el-form-item>
        <el-form-item v-if="dialog.mode === 'create'" label="空间配额">
          <el-input-number v-model="form.quotaGB" :min="0" :max="1024000" style="width: 160px" />
          <span class="ly-muted" style="margin-left: 10px">GB，0 表示不限</span>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="form.sort" :min="0" :max="9999" style="width: 160px" />
        </el-form-item>
        <el-form-item v-if="dialog.mode === 'edit'" label="状态">
          <el-switch v-model="form.enabled" active-text="启用" inactive-text="停用" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="form.remark" type="textarea" :rows="2" placeholder="选填" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.submitting" @click="submit">
          {{ dialog.mode === 'create' ? '创建' : '保存' }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.ly-dept-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}
.ly-dept-name {
  font-size: 14px;
}
</style>
