<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api } from '@/api'
import { useUserStore } from '@/stores/user'
import { formatTime, humanSize } from '@/utils/format'

const store = useUserStore()

const profile = reactive({
  nickname: store.user?.nickname ?? '',
  email: store.user?.email ?? '',
  phone: store.user?.phone ?? '',
})
const pwd = reactive({ old_password: '', new_password: '', confirm: '' })
const savingProfile = ref(false)
const savingPwd = ref(false)

async function saveProfile() {
  if (!profile.nickname.trim()) {
    ElMessage.warning('姓名不能为空')
    return
  }
  savingProfile.value = true
  try {
    await api.updateProfile({
      nickname: profile.nickname.trim(),
      email: profile.email.trim(),
      phone: profile.phone.trim(),
    })
    await store.loadProfile()
    ElMessage.success('资料已保存')
  } finally {
    savingProfile.value = false
  }
}

async function savePassword() {
  if (!pwd.old_password || !pwd.new_password) {
    ElMessage.warning('请填写原口令与新口令')
    return
  }
  if (pwd.new_password !== pwd.confirm) {
    ElMessage.warning('两次输入的新口令不一致')
    return
  }
  savingPwd.value = true
  try {
    await api.changePassword(pwd.old_password, pwd.new_password)
    pwd.old_password = ''
    pwd.new_password = ''
    pwd.confirm = ''
    await store.loadProfile()
    ElMessage.success('口令已修改')
  } finally {
    savingPwd.value = false
  }
}
</script>

<template>
  <div class="ly-page">
    <div class="ly-page-head">
      <div>
        <h1 class="ly-page-title">个人设置</h1>
        <p class="ly-page-desc">部门、角色与配额由超级管理员统一维护，如需调整请联系管理员。</p>
      </div>
    </div>

    <div class="ly-profile">
      <!-- 账号概览 -->
      <div class="ly-card ly-card-pad ly-profile-summary">
        <div class="ly-profile-avatar">{{ store.displayName.slice(0, 1) }}</div>
        <div class="ly-profile-meta">
          <h3>{{ store.displayName }}</h3>
          <p class="ly-muted">{{ store.user?.username }}</p>
          <div class="ly-profile-tags">
            <span class="ly-tag ly-tag--primary">{{ store.user?.role_label }}</span>
            <span class="ly-tag">{{ store.user?.dept_path || store.user?.dept_name || '未分配部门' }}</span>
          </div>
        </div>
        <dl class="ly-profile-facts">
          <div>
            <dt>个人容量</dt>
            <dd>
              {{ humanSize(store.personalSpace?.used_bytes ?? 0) }}
              <span class="ly-muted">
                /
                {{ store.user?.quota_bytes ? humanSize(store.user.quota_bytes) : '不限' }}
              </span>
            </dd>
          </div>
          <div>
            <dt>上次登录</dt>
            <dd>{{ formatTime(store.user?.last_login_at) }}</dd>
          </div>
          <div>
            <dt>登录 IP</dt>
            <dd>{{ store.user?.last_login_ip || '—' }}</dd>
          </div>
          <div>
            <dt>开通人</dt>
            <dd>{{ store.user?.creator_name || '系统初始化' }}</dd>
          </div>
        </dl>
      </div>

      <div class="ly-profile-cols">
        <!-- 基本资料 -->
        <section class="ly-card ly-card-pad">
          <h3 class="ly-section-title">基本资料</h3>
          <el-form label-width="76px" label-position="left">
            <el-form-item label="姓名">
              <el-input v-model="profile.nickname" maxlength="32" />
            </el-form-item>
            <el-form-item label="邮箱">
              <el-input v-model="profile.email" placeholder="选填" />
            </el-form-item>
            <el-form-item label="手机">
              <el-input v-model="profile.phone" placeholder="选填" />
            </el-form-item>
            <el-button type="primary" :loading="savingProfile" @click="saveProfile">保存资料</el-button>
          </el-form>
        </section>

        <!-- 修改口令 -->
        <section class="ly-card ly-card-pad">
          <h3 class="ly-section-title">
            修改口令
            <span v-if="store.needsPasswordChange" class="ly-tag ly-tag--warning">仍在使用初始口令</span>
          </h3>
          <el-form label-width="76px" label-position="left">
            <el-form-item label="原口令">
              <el-input v-model="pwd.old_password" type="password" show-password autocomplete="current-password" />
            </el-form-item>
            <el-form-item label="新口令">
              <el-input v-model="pwd.new_password" type="password" show-password autocomplete="new-password" />
            </el-form-item>
            <el-form-item label="确认">
              <el-input
                v-model="pwd.confirm"
                type="password"
                show-password
                autocomplete="new-password"
                @keyup.enter="savePassword"
              />
            </el-form-item>
            <el-button type="primary" :loading="savingPwd" @click="savePassword">修改口令</el-button>
          </el-form>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ly-profile {
  display: grid;
  gap: 18px;
  max-width: 1100px;
}

.ly-profile-summary {
  display: flex;
  align-items: center;
  gap: 22px;
  flex-wrap: wrap;
}
.ly-profile-avatar {
  width: 62px;
  height: 62px;
  border-radius: 18px;
  background: var(--ly-primary);
  color: #fff;
  font-size: 24px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.ly-profile-meta h3 {
  margin: 0 0 2px;
  font-size: 18px;
  font-weight: 600;
}
.ly-profile-meta p {
  margin: 0 0 8px;
  font-size: 13px;
}
.ly-profile-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.ly-profile-facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 18px 28px;
  margin: 0 0 0 auto;
  padding: 0;
}
.ly-profile-facts dt {
  font-size: 12px;
  color: var(--ly-text-tertiary);
  margin-bottom: 3px;
}
.ly-profile-facts dd {
  margin: 0;
  font-size: 14px;
  color: var(--ly-text);
}

.ly-profile-cols {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
  gap: 18px;
  align-items: start;
}

.ly-section-title {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0 0 18px;
  font-size: 15px;
  font-weight: 600;
}
</style>
