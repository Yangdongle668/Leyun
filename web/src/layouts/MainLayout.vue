<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { useUserStore } from '@/stores/user'
import { humanSize, spaceTypeLabel } from '@/utils/format'

const store = useUserStore()
const router = useRouter()
const route = useRoute()

const MOBILE_WIDTH = 768
const isMobile = ref(false)
// 手机上侧栏是覆盖式的，默认必须收起，否则一进来就挡住整个内容区。
const collapsed = ref(false)

function syncViewport() {
  const mobile = window.innerWidth <= MOBILE_WIDTH
  if (mobile !== isMobile.value) {
    isMobile.value = mobile
    collapsed.value = mobile
  }
}

onMounted(() => {
  isMobile.value = window.innerWidth <= MOBILE_WIDTH
  collapsed.value = isMobile.value
  window.addEventListener('resize', syncViewport)
})
onUnmounted(() => window.removeEventListener('resize', syncViewport))

// 手机上跳转后自动收起侧栏，省一次手动点击。
watch(
  () => route.fullPath,
  () => {
    if (isMobile.value) collapsed.value = true
  },
)

const activeKey = computed(() => {
  if (route.name === 'files') {
    return `space-${route.params.spaceId ?? store.personalSpace?.id ?? ''}`
  }
  return String(route.name ?? '')
})

const personal = computed(() => store.personalSpace)
const quotaPercent = computed(() => {
  const sp = personal.value
  if (!sp || !sp.quota_bytes) return 0
  return Math.min(100, Math.round((sp.used_bytes / sp.quota_bytes) * 100))
})

function goSpace(id: number) {
  router.push({ name: 'files', params: { spaceId: String(id) } })
}

async function handleLogout() {
  await ElMessageBox.confirm('确定要退出登录吗？', '退出登录', {
    confirmButtonText: '退出',
    cancelButtonText: '取消',
    type: 'warning',
  })
  await store.logout()
  router.push({ name: 'login' })
}

function onCommand(cmd: string) {
  if (cmd === 'logout') handleLogout()
  else if (cmd === 'profile') router.push({ name: 'profile' })
}
</script>

<template>
  <div class="ly-shell">
    <div v-if="isMobile && !collapsed" class="ly-backdrop" @click="collapsed = true"></div>
    <aside class="ly-sidebar" :class="{ 'is-collapsed': collapsed }">
      <div class="ly-brand" @click="router.push('/files')">
        <span class="ly-brand-mark">乐</span>
        <span v-show="!collapsed" class="ly-brand-text">{{ store.siteName }}</span>
      </div>

      <nav class="ly-nav">
        <div v-show="!collapsed" class="ly-nav-label">我的空间</div>
        <button
          v-if="personal"
          class="ly-nav-item"
          :class="{ 'is-active': activeKey === `space-${personal.id}` }"
          :title="personal.name"
          @click="goSpace(personal.id)"
        >
          <el-icon><User /></el-icon>
          <span v-show="!collapsed">个人空间</span>
        </button>

        <template v-if="store.deptSpaces.length">
          <div v-show="!collapsed" class="ly-nav-label">部门空间</div>
          <button
            v-for="sp in store.deptSpaces"
            :key="sp.id"
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === `space-${sp.id}` }"
            :title="sp.name"
            @click="goSpace(sp.id)"
          >
            <el-icon><OfficeBuilding /></el-icon>
            <span v-show="!collapsed" class="ly-nav-text">{{ sp.name }}</span>
          </button>
        </template>

        <template v-if="store.publicSpaces.length">
          <div v-show="!collapsed" class="ly-nav-label">公共空间</div>
          <button
            v-for="sp in store.publicSpaces"
            :key="sp.id"
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === `space-${sp.id}` }"
            :title="sp.name"
            @click="goSpace(sp.id)"
          >
            <el-icon><Share /></el-icon>
            <span v-show="!collapsed" class="ly-nav-text">{{ sp.name }}</span>
          </button>
        </template>

        <template v-if="store.sharedSpaces.length">
          <div v-show="!collapsed" class="ly-nav-label">共享给我</div>
          <button
            v-for="sp in store.sharedSpaces"
            :key="sp.id"
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === `space-${sp.id}` }"
            :title="`${sp.name}（${spaceTypeLabel(sp.type)}）`"
            @click="goSpace(sp.id)"
          >
            <el-icon><FolderOpened /></el-icon>
            <span v-show="!collapsed" class="ly-nav-text">{{ sp.name }}</span>
          </button>
        </template>

        <div class="ly-nav-divider"></div>

        <button
          class="ly-nav-item"
          :class="{ 'is-active': activeKey === 'shares' }"
          @click="router.push({ name: 'shares' })"
        >
          <el-icon><Link /></el-icon>
          <span v-show="!collapsed">我的分享</span>
        </button>
        <button
          class="ly-nav-item"
          :class="{ 'is-active': activeKey === 'trash' }"
          @click="router.push({ name: 'trash' })"
        >
          <el-icon><Delete /></el-icon>
          <span v-show="!collapsed">回收站</span>
        </button>

        <template v-if="store.isAdmin">
          <div class="ly-nav-divider"></div>
          <div v-show="!collapsed" class="ly-nav-label">管理后台</div>
          <button
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === 'admin-overview' }"
            @click="router.push({ name: 'admin-overview' })"
          >
            <el-icon><DataLine /></el-icon>
            <span v-show="!collapsed">概览</span>
          </button>
          <button
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === 'admin-users' }"
            @click="router.push({ name: 'admin-users' })"
          >
            <el-icon><UserFilled /></el-icon>
            <span v-show="!collapsed">账号管理</span>
          </button>
          <button
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === 'admin-departments' }"
            @click="router.push({ name: 'admin-departments' })"
          >
            <el-icon><Connection /></el-icon>
            <span v-show="!collapsed">部门管理</span>
          </button>
          <button
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === 'admin-spaces' }"
            @click="router.push({ name: 'admin-spaces' })"
          >
            <el-icon><Coin /></el-icon>
            <span v-show="!collapsed">空间管理</span>
          </button>
          <button
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === 'admin-audit' }"
            @click="router.push({ name: 'admin-audit' })"
          >
            <el-icon><Tickets /></el-icon>
            <span v-show="!collapsed">审计日志</span>
          </button>
          <button
            v-if="store.isSuperAdmin"
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === 'admin-api-keys' }"
            @click="router.push({ name: 'admin-api-keys' })"
          >
            <el-icon><Key /></el-icon>
            <span v-show="!collapsed">API 密钥</span>
          </button>
          <button
            v-if="store.isSuperAdmin"
            class="ly-nav-item"
            :class="{ 'is-active': activeKey === 'admin-settings' }"
            @click="router.push({ name: 'admin-settings' })"
          >
            <el-icon><Setting /></el-icon>
            <span v-show="!collapsed">系统设置</span>
          </button>
        </template>
      </nav>

      <div v-if="personal && !collapsed" class="ly-quota">
        <div class="ly-quota-head">
          <span>个人容量</span>
          <span class="ly-quota-num">
            {{ humanSize(personal.used_bytes) }}
            <template v-if="personal.quota_bytes"> / {{ humanSize(personal.quota_bytes) }}</template>
          </span>
        </div>
        <div v-if="personal.quota_bytes" class="ly-quota-bar">
          <div
            class="ly-quota-fill"
            :class="{ 'is-warn': quotaPercent >= 85 }"
            :style="{ width: `${quotaPercent}%` }"
          ></div>
        </div>
        <div v-else class="ly-quota-num">不限容量</div>
      </div>
    </aside>

    <div class="ly-main">
      <header class="ly-header">
        <button class="ly-icon-btn" :title="collapsed ? '展开侧栏' : '收起侧栏'" @click="collapsed = !collapsed">
          <el-icon><Fold v-if="!collapsed" /><Expand v-else /></el-icon>
        </button>
        <h2 class="ly-header-title">{{ route.meta.title || '文件' }}</h2>
        <div class="ly-spacer"></div>

        <el-dropdown trigger="click" @command="onCommand">
          <button class="ly-user">
            <span class="ly-avatar">{{ store.displayName.slice(0, 1) }}</span>
            <span class="ly-user-meta">
              <span class="ly-user-name">{{ store.displayName }}</span>
              <span class="ly-user-role">{{ store.user?.role_label }}</span>
            </span>
            <el-icon class="ly-muted"><ArrowDown /></el-icon>
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="profile">
                <el-icon><Setting /></el-icon> 个人设置
              </el-dropdown-item>
              <el-dropdown-item command="logout" divided>
                <el-icon><SwitchButton /></el-icon> 退出登录
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </header>

      <div v-if="store.needsPasswordChange" class="ly-banner">
        <el-icon><WarnTriangleFilled /></el-icon>
        <span>当前账号仍在使用初始口令，存在安全风险。</span>
        <router-link to="/profile">立即修改 →</router-link>
      </div>

      <main class="ly-content">
        <router-view />
      </main>
    </div>
  </div>
</template>

<style scoped>
.ly-shell {
  display: flex;
  height: 100%;
  background: var(--ly-bg);
}

/* ---------- 侧栏 ---------- */
.ly-sidebar {
  width: var(--ly-sidebar-width);
  flex-shrink: 0;
  background: var(--ly-sidebar-bg);
  display: flex;
  flex-direction: column;
  transition: width 0.2s ease;
  overflow: hidden;
}
.ly-sidebar.is-collapsed {
  width: var(--ly-sidebar-collapsed);
}

.ly-brand {
  height: var(--ly-header-height);
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 18px;
  cursor: pointer;
  flex-shrink: 0;
}
.ly-brand-mark {
  width: 28px;
  height: 28px;
  flex-shrink: 0;
  border-radius: 8px;
  background: var(--ly-primary);
  color: #fff;
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
}
.ly-brand-text {
  color: #fff;
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.4px;
  white-space: nowrap;
}

.ly-nav {
  flex: 1;
  overflow-y: auto;
  padding: 6px 10px 16px;
}
.ly-nav::-webkit-scrollbar-thumb {
  background: rgba(255, 255, 255, 0.12);
}

.ly-nav-label {
  padding: 14px 10px 6px;
  font-size: 11px;
  letter-spacing: 1px;
  color: #5c6679;
  text-transform: uppercase;
}

.ly-nav-item {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  height: 38px;
  padding: 0 10px;
  margin-bottom: 2px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--ly-sidebar-text);
  font-size: 14px;
  font-family: inherit;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s, color 0.15s;
}
.ly-nav-item:hover {
  background: rgba(255, 255, 255, 0.06);
  color: #fff;
}
.ly-nav-item.is-active {
  background: var(--ly-sidebar-active-bg);
  color: var(--ly-sidebar-text-active);
  font-weight: 500;
}
.ly-nav-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ly-nav-divider {
  height: 1px;
  margin: 12px 10px;
  background: rgba(255, 255, 255, 0.07);
}

.ly-quota {
  padding: 14px 18px 18px;
  border-top: 1px solid rgba(255, 255, 255, 0.07);
  flex-shrink: 0;
}
.ly-quota-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  font-size: 12px;
  color: #7b8699;
  margin-bottom: 8px;
}
.ly-quota-num {
  font-size: 11px;
  color: #98a3b6;
}
.ly-quota-bar {
  height: 4px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.1);
  overflow: hidden;
}
.ly-quota-fill {
  height: 100%;
  background: var(--ly-primary);
  border-radius: 4px;
  transition: width 0.3s ease;
}
.ly-quota-fill.is-warn {
  background: var(--ly-warning);
}

/* ---------- 主区 ---------- */
.ly-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.ly-header {
  height: var(--ly-header-height);
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 20px 0 12px;
  background: var(--ly-surface);
  border-bottom: 1px solid var(--ly-border);
}

.ly-header-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: var(--ly-text);
}

.ly-icon-btn {
  width: 34px;
  height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: var(--ly-text-secondary);
  cursor: pointer;
  font-size: 17px;
}
.ly-icon-btn:hover {
  background: var(--ly-surface-sunken);
  color: var(--ly-text);
}

.ly-user {
  display: flex;
  align-items: center;
  gap: 9px;
  height: 40px;
  padding: 0 10px 0 6px;
  border: none;
  border-radius: 10px;
  background: transparent;
  cursor: pointer;
  font-family: inherit;
}
.ly-user:hover {
  background: var(--ly-surface-sunken);
}
.ly-avatar {
  width: 30px;
  height: 30px;
  border-radius: 9px;
  background: var(--ly-primary);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  display: flex;
  align-items: center;
  justify-content: center;
}
.ly-user-meta {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  line-height: 1.25;
}
.ly-user-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--ly-text);
}
.ly-user-role {
  font-size: 11px;
  color: var(--ly-text-tertiary);
}

.ly-banner {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 22px;
  background: #fff8e8;
  border-bottom: 1px solid #f6e3bd;
  color: #9a6b12;
  font-size: 13px;
}
.ly-banner a {
  color: #9a6b12;
  font-weight: 600;
}

.ly-content {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.ly-backdrop {
  position: fixed;
  inset: 0;
  z-index: 99;
  background: rgba(15, 23, 42, 0.38);
}

@media (max-width: 768px) {
  .ly-sidebar {
    position: fixed;
    z-index: 100;
    height: 100%;
    box-shadow: var(--ly-shadow-lg);
  }
  .ly-sidebar.is-collapsed {
    width: 0;
  }
  .ly-user-meta {
    display: none;
  }
}
</style>
