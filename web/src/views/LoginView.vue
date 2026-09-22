<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import LogoMark from '@/components/LogoMark.vue'
import ParticleField from '@/components/ParticleField.vue'
import { api } from '@/api'
import { useUserStore } from '@/stores/user'

const router = useRouter()
const route = useRoute()
const store = useUserStore()

const form = reactive({ username: '', password: '' })
const loading = ref(false)
const siteName = ref('乐云企业网盘')
const notice = ref('本系统不开放自助注册，账号由超级管理员统一开通')

onMounted(async () => {
  try {
    const info = await api.siteInfo()
    siteName.value = info.settings.site_name || siteName.value
    notice.value = info.register_notice || notice.value
  } catch {
    // 站点信息拿不到不影响登录，沿用默认文案。
  }
})

async function submit() {
  if (!form.username.trim() || !form.password) {
    ElMessage.warning('请输入用户名与口令')
    return
  }
  loading.value = true
  try {
    const res = await store.login(form.username.trim(), form.password)
    const redirect = (route.query.redirect as string) || '/files'
    await router.push(redirect)
    if (res.must_reset_password) {
      ElMessage.warning('当前账号仍在使用初始口令，请尽快修改')
    }
  } catch {
    // 错误提示已由拦截器统一弹出。
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="ly-login">
    <!-- 左侧品牌区：标识 + 一句标语，别的什么都不放 -->
    <section class="ly-login-brand">
      <!-- 微粒连线背景。放在内容之前，自然落在下层 -->
      <ParticleField />
      <div class="ly-login-brand-inner">
        <div class="ly-login-logo">
          <LogoMark :size="34" />
          <span class="name">{{ siteName }}</span>
        </div>
        <h1>让文件在部门之间<br />有序流动</h1>
      </div>
    </section>

    <!-- 右侧表单区 -->
    <section class="ly-login-form">
      <div class="ly-login-card">
        <h2>登录</h2>
        <p class="ly-login-sub">使用企业分配的账号登录</p>

        <el-form :model="form" size="large" @submit.prevent="submit">
          <el-form-item>
            <el-input
              v-model="form.username"
              placeholder="用户名"
              autocomplete="username"
              :prefix-icon="'User'"
              clearable
            />
          </el-form-item>
          <el-form-item>
            <el-input
              v-model="form.password"
              type="password"
              placeholder="口令"
              autocomplete="current-password"
              :prefix-icon="'Lock'"
              show-password
              @keyup.enter="submit"
            />
          </el-form-item>
          <el-button type="primary" class="ly-login-btn" :loading="loading" @click="submit">
            登 录
          </el-button>
        </el-form>

        <div class="ly-login-notice">
          <el-icon><InfoFilled /></el-icon>
          <span>{{ notice }}</span>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.ly-login {
  display: flex;
  min-height: 100vh;
  background: var(--ly-surface);
}

/* ---------- 品牌区 ---------- */
.ly-login-brand {
  flex: 1.1;
  position: relative;
  display: flex;
  align-items: center;
  padding: 64px;
  background: linear-gradient(150deg, #101a30 0%, #16264a 46%, #1f3f8f 100%);
  color: #fff;
  overflow: hidden;
}
/* 一层极淡的光晕，避免大块纯色显得死板 */
.ly-login-brand::after {
  content: '';
  position: absolute;
  width: 620px;
  height: 620px;
  right: -180px;
  top: -160px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(37, 99, 240, 0.38) 0%, rgba(37, 99, 240, 0) 68%);
  pointer-events: none;
}
.ly-login-brand-inner {
  position: relative;
  /* 高于粒子层，否则文字会被点和线盖住 */
  z-index: 2;
  max-width: 460px;
}
.ly-login-logo {
  display: flex;
  align-items: center;
  gap: var(--ly-space-3);
  margin-bottom: 48px;
  /* 标识用 currentColor，在深色底上就是白的 */
  color: #fff;
}
.ly-login-logo .name {
  font-size: var(--ly-font-lg);
  font-weight: 600;
  letter-spacing: 0.01em;
}
/*
 * 整块只剩标语，字号可以放开。
 * clamp 让它在窄屏收到 30px、宽屏放到 46px，不用写一堆断点。
 */
.ly-login-brand h1 {
  margin: 0;
  font-size: clamp(30px, 3.4vw, 46px);
  line-height: 1.3;
  font-weight: 600;
  letter-spacing: -0.01em;
}

/* ---------- 表单区 ---------- */
.ly-login-form {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 48px 32px;
  min-width: 380px;
}
.ly-login-card {
  width: 100%;
  max-width: 348px;
}
.ly-login-card h2 {
  margin: 0 0 6px;
  font-size: 26px;
  font-weight: 600;
  letter-spacing: 0.5px;
}
.ly-login-sub {
  margin: 0 0 32px;
  font-size: 14px;
  color: var(--ly-text-tertiary);
}
.ly-login-btn {
  width: 100%;
  height: 44px;
  font-size: 15px;
  letter-spacing: 4px;
  border-radius: 10px;
  margin-top: 4px;
}
.ly-login-notice {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-top: 28px;
  padding: 12px 14px;
  border-radius: 10px;
  background: var(--ly-surface-sunken);
  border: 1px solid var(--ly-border);
  font-size: 12.5px;
  line-height: 1.7;
  color: var(--ly-text-tertiary);
}

@media (max-width: 900px) {
  .ly-login-brand {
    display: none;
  }
  .ly-login-form {
    min-width: 0;
  }
}
</style>
