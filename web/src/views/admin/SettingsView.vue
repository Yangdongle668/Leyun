<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '@/api'
import { useUserStore } from '@/stores/user'
import { bytesToGB, gbToBytes, humanSize } from '@/utils/format'

const store = useUserStore()
const loading = ref(false)
const saving = ref(false)

const meta = reactive({
  password_min: 5,
  chunk_size: 0,
  max_upload_size: 0,
  office_enabled: false,
})

const form = reactive({
  siteName: '',
  defaultUserQuotaGB: 10,
  defaultDeptQuotaGB: 100,
  allowPublicShare: true,
  trashRetentionDays: 30,
})

async function load() {
  loading.value = true
  try {
    const res = await api.settings()
    meta.password_min = res.password_min
    meta.chunk_size = res.chunk_size
    meta.max_upload_size = res.max_upload_size
    meta.office_enabled = res.office_enabled
    const s = res.settings
    form.siteName = s['system.site_name'] || '乐云企业网盘'
    form.defaultUserQuotaGB = bytesToGB(Number(s['system.default_user_quota'] || 0))
    form.defaultDeptQuotaGB = bytesToGB(Number(s['system.default_dept_quota'] || 0))
    form.allowPublicShare = s['system.allow_public_share'] !== 'false'
    form.trashRetentionDays = Number(s['system.trash_retention_days'] || 30)
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function save() {
  saving.value = true
  try {
    await api.updateSettings({
      'system.site_name': form.siteName.trim() || '乐云企业网盘',
      'system.default_user_quota': String(gbToBytes(form.defaultUserQuotaGB)),
      'system.default_dept_quota': String(gbToBytes(form.defaultDeptQuotaGB)),
      'system.allow_public_share': String(form.allowPublicShare),
      'system.trash_retention_days': String(form.trashRetentionDays),
    })
    await store.loadProfile()
    ElMessage.success('设置已保存')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="ly-page" v-loading="loading">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">系统设置</h1>
        <p class="ly-page-desc">这些设置立即生效，无需重启服务。</p>
      </div>
      <el-button type="primary" :loading="saving" @click="save">保存设置</el-button>
    </div>

    <div class="ly-settings">
      <section class="ly-card ly-card-pad">
        <h3 class="ly-section-title">站点</h3>
        <el-form label-width="140px" label-position="left">
          <el-form-item label="站点名称">
            <el-input v-model="form.siteName" maxlength="32" style="max-width: 320px" />
            <div class="ly-hint">显示在登录页与侧边栏顶部。</div>
          </el-form-item>
        </el-form>
      </section>

      <section class="ly-card ly-card-pad">
        <h3 class="ly-section-title">默认配额</h3>
        <el-form label-width="140px" label-position="left">
          <el-form-item label="新账号个人空间">
            <el-input-number v-model="form.defaultUserQuotaGB" :min="0" :max="102400" style="width: 160px" />
            <span class="ly-muted" style="margin-left: 10px">GB，0 表示不限</span>
            <div class="ly-hint">开通账号时若未单独指定配额，使用该值。</div>
          </el-form-item>
          <el-form-item label="新部门空间">
            <el-input-number v-model="form.defaultDeptQuotaGB" :min="0" :max="1024000" style="width: 160px" />
            <span class="ly-muted" style="margin-left: 10px">GB，0 表示不限</span>
          </el-form-item>
        </el-form>
      </section>

      <section class="ly-card ly-card-pad">
        <h3 class="ly-section-title">分享与回收站</h3>
        <el-form label-width="140px" label-position="left">
          <el-form-item label="对外公开分享">
            <el-switch v-model="form.allowPublicShare" active-text="允许" inactive-text="禁止" />
            <div class="ly-hint">
              关闭后，成员只能创建"仅企业内部"的分享链接，已有的公开链接不受影响。
            </div>
          </el-form-item>
          <el-form-item label="回收站保留天数">
            <el-input-number v-model="form.trashRetentionDays" :min="0" :max="3650" style="width: 160px" />
            <span class="ly-muted" style="margin-left: 10px">天，0 表示不自动清理</span>
          </el-form-item>
        </el-form>
      </section>

      <section class="ly-card ly-card-pad">
        <h3 class="ly-section-title">运行参数</h3>
        <p class="ly-hint" style="margin-top: -8px">
          以下参数来自服务端配置文件（config.yaml）或环境变量，需重启服务后生效。
        </p>
        <dl class="ly-facts">
          <div>
            <dt>口令最小长度</dt>
            <dd>{{ meta.password_min }} 位</dd>
          </div>
          <div>
            <dt>分片大小</dt>
            <dd>{{ humanSize(meta.chunk_size) }}</dd>
          </div>
          <div>
            <dt>单文件上限</dt>
            <dd>{{ meta.max_upload_size ? humanSize(meta.max_upload_size) : '不限' }}</dd>
          </div>
          <div>
            <dt>Office 在线编辑</dt>
            <dd>
              <span class="ly-tag" :class="meta.office_enabled ? 'ly-tag--success' : ''">
                {{ meta.office_enabled ? '已启用' : '未启用' }}
              </span>
            </dd>
          </div>
        </dl>
      </section>

      <section class="ly-card ly-card-pad">
        <h3 class="ly-section-title">账号策略</h3>
        <div class="ly-policy">
          <el-icon color="#1f5eff" :size="18"><InfoFilled /></el-icon>
          <div>
            <p><strong>本系统不提供自助注册。</strong></p>
            <p class="ly-hint">
              所有账号只能由超级管理员在「账号管理」中开通；<code>/auth/register</code>
              等注册接口会直接返回拒绝。部门管理员可以管理本部门及下级空间的目录与权限，但同样无法开通账号。
            </p>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.ly-settings {
  display: grid;
  gap: 18px;
  max-width: 820px;
}
.ly-section-title {
  margin: 0 0 18px;
  font-size: 15px;
  font-weight: 600;
}
.ly-hint {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  line-height: 1.8;
}
.ly-facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 16px 24px;
  margin: 16px 0 0;
  padding: 0;
}
.ly-facts dt {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  margin-bottom: 4px;
}
.ly-facts dd {
  margin: 0;
  font-size: 14px;
}
.ly-policy {
  display: flex;
  gap: 12px;
  padding: 14px 16px;
  border-radius: var(--ly-radius);
  background: var(--ly-primary-soft);
  border: 1px solid var(--ly-primary-border);
}
.ly-policy p {
  margin: 0 0 4px;
  font-size: 13px;
}
.ly-policy code {
  background: rgba(255, 255, 255, 0.7);
  padding: 1px 5px;
  border-radius: 4px;
  font-size: 12px;
}
</style>
