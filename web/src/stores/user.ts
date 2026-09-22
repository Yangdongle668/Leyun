import { defineStore } from 'pinia'
import { api, clearToken, setToken } from '@/api'
import type { PermissionOption, Profile, Space, User } from '@/api/types'

interface State {
  user: User | null
  spaces: Space[]
  permissions: PermissionOption[]
  settings: Record<string, string>
  officeEnabled: boolean
  /** PDF 的内置阅读器不依赖 Document Server，但"在线编辑 PDF"依赖它。 */
  pdfEditEnabled: boolean
  loaded: boolean
}

export const useUserStore = defineStore('user', {
  state: (): State => ({
    user: null,
    spaces: [],
    permissions: [],
    settings: {},
    officeEnabled: false,
    pdfEditEnabled: false,
    loaded: false,
  }),

  getters: {
    isLoggedIn: (s) => !!s.user,
    isSuperAdmin: (s) => s.user?.role === 'super_admin',
    isAdmin: (s) => s.user?.role === 'super_admin' || s.user?.role === 'dept_admin',
    displayName: (s) => s.user?.nickname || s.user?.username || '',
    /** 顶部横幅：仍在用初始口令时持续提醒。 */
    needsPasswordChange: (s) => !!s.user?.must_change_password,
    siteName: (s) => s.settings.site_name || '乐云企业网盘',
    personalSpace: (s) => s.spaces.find((sp) => sp.type === 'personal' && sp.owner_id === s.user?.id),
    deptSpaces: (s) => s.spaces.filter((sp) => sp.type === 'department'),
    publicSpaces: (s) => s.spaces.filter((sp) => sp.type === 'public'),
    sharedSpaces(s): Space[] {
      // 别人开给我的个人空间：既不是我的，也不是部门/公共空间。
      return s.spaces.filter((sp) => sp.type === 'personal' && sp.owner_id !== s.user?.id)
    },
  },

  actions: {
    async login(username: string, password: string) {
      const res = await api.login(username, password)
      setToken(res.token)
      this.user = res.user
      this.permissions = res.permissions
      await this.loadProfile()
      return res
    },

    async loadProfile(): Promise<Profile | null> {
      const profile = await api.profile()
      this.user = profile.user
      this.spaces = profile.spaces
      this.permissions = profile.permissions
      this.settings = profile.settings || {}
      this.officeEnabled = profile.office_enabled
      this.pdfEditEnabled = profile.pdf_edit_enabled
      this.loaded = true
      return profile
    },

    async refreshSpaces() {
      this.spaces = await api.spaces()
    },

    async logout() {
      await api.logout().catch(() => undefined)
      this.reset()
    },

    reset() {
      clearToken()
      this.user = null
      this.spaces = []
      this.loaded = false
    },
  },
})
