import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { getToken } from '@/api'
import { useUserStore } from '@/stores/user'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true, blank: true, title: '登录' },
  },
  {
    // 公开分享页：不要求登录。
    path: '/s/:code',
    name: 'share',
    component: () => import('@/views/ShareView.vue'),
    meta: { public: true, blank: true, title: '文件分享' },
  },
  {
    path: '/office/:spaceId/:nodeId',
    name: 'office',
    component: () => import('@/views/OfficeView.vue'),
    meta: { blank: true, title: '在线编辑' },
  },
  {
    path: '/',
    component: () => import('@/layouts/MainLayout.vue'),
    children: [
      { path: '', redirect: '/files' },
      {
        path: 'files/:spaceId?',
        name: 'files',
        component: () => import('@/views/FilesView.vue'),
        meta: { title: '文件' },
      },
      {
        path: 'trash',
        name: 'trash',
        component: () => import('@/views/TrashView.vue'),
        meta: { title: '回收站' },
      },
      {
        path: 'shares',
        name: 'shares',
        component: () => import('@/views/SharesView.vue'),
        meta: { title: '我的分享' },
      },
      {
        path: 'profile',
        name: 'profile',
        component: () => import('@/views/ProfileView.vue'),
        meta: { title: '个人设置' },
      },
      {
        path: 'admin/overview',
        name: 'admin-overview',
        component: () => import('@/views/admin/OverviewView.vue'),
        meta: { title: '概览', admin: true },
      },
      {
        path: 'admin/users',
        name: 'admin-users',
        component: () => import('@/views/admin/UsersView.vue'),
        meta: { title: '账号管理', admin: true },
      },
      {
        path: 'admin/departments',
        name: 'admin-departments',
        component: () => import('@/views/admin/DepartmentsView.vue'),
        meta: { title: '部门管理', admin: true },
      },
      {
        path: 'admin/spaces',
        name: 'admin-spaces',
        component: () => import('@/views/admin/SpacesView.vue'),
        meta: { title: '空间管理', admin: true },
      },
      {
        path: 'admin/audit',
        name: 'admin-audit',
        component: () => import('@/views/admin/AuditView.vue'),
        meta: { title: '审计日志', admin: true },
      },
      {
        path: 'admin/api-keys',
        name: 'admin-api-keys',
        component: () => import('@/views/admin/ApiKeysView.vue'),
        meta: { title: 'API 密钥', superAdmin: true },
      },
      {
        path: 'admin/settings',
        name: 'admin-settings',
        component: () => import('@/views/admin/SettingsView.vue'),
        meta: { title: '系统设置', superAdmin: true },
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/views/NotFoundView.vue'),
    meta: { public: true, blank: true, title: '页面不存在' },
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
})

router.beforeEach(async (to) => {
  const title = (to.meta.title as string) || ''
  document.title = title ? `${title} · 乐云企业网盘` : '乐云企业网盘'

  if (to.meta.public) return true

  if (!getToken()) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }

  const store = useUserStore()
  if (!store.loaded) {
    try {
      await store.loadProfile()
    } catch {
      store.reset()
      return { name: 'login', query: { redirect: to.fullPath } }
    }
  }

  if (to.meta.superAdmin && !store.isSuperAdmin) {
    return { name: 'files' }
  }
  if (to.meta.admin && !store.isAdmin) {
    return { name: 'files' }
  }
  return true
})

export default router
