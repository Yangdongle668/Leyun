import { get, post, put, del, type PageBody } from './request'
import type {
  AccessRule,
  AuditLog,
  DeptNode,
  Department,
  FileNode,
  ListResult,
  OfficeEditorConfig,
  Overview,
  PermCode,
  PermissionOption,
  PrincipalType,
  Profile,
  Share,
  Space,
  TrashItem,
  User,
} from './types'

/* ---------------- 认证 ---------------- */

export interface LoginResult {
  token: string
  expire_at: string
  user: User
  must_reset_password: boolean
  permissions: PermissionOption[]
}

export const api = {
  siteInfo: () =>
    get<{ settings: Record<string, string>; allow_register: boolean; register_notice: string; office_enabled: boolean }>(
      '/site',
    ),
  login: (username: string, password: string) =>
    post<LoginResult>('/auth/login', { username, password }),
  logout: () => post('/auth/logout'),
  profile: () => get<Profile>('/auth/profile'),
  updateProfile: (data: { nickname?: string; email?: string; phone?: string }) =>
    put<User>('/auth/profile', data),
  changePassword: (old_password: string, new_password: string) =>
    post('/auth/password', { old_password, new_password }),

  /* ---------------- 空间与文件 ---------------- */

  spaces: () => get<Space[]>('/spaces'),
  listFiles: (params: { space_id: number; parent_id?: number; keyword?: string; order_by?: string }) =>
    get<ListResult>('/files', params),
  mkdir: (space_id: number, parent_id: number, name: string) =>
    post<FileNode>('/files/folder', { space_id, parent_id, name }),
  rename: (space_id: number, node_id: number, name: string) =>
    post<FileNode>('/files/rename', { space_id, node_id, name }),
  move: (data: { space_id: number; node_ids: number[]; target_space_id?: number; target_id: number }) =>
    post<{ moved: number }>('/files/move', data),
  copy: (data: { space_id: number; node_ids: number[]; target_space_id?: number; target_id: number }) =>
    post<{ copied: number }>('/files/copy', data),
  trash: (space_id: number, node_ids: number[]) =>
    post<{ trashed: number }>('/files/trash', { space_id, node_ids }),

  /* ---------------- 上传 ---------------- */

  initUpload: (data: { space_id: number; parent_id: number; filename: string; size: number; hash?: string }) =>
    post<{
      instant: boolean
      node?: FileNode
      upload_id?: string
      chunk_size?: number
      chunk_count?: number
      uploaded: number[]
    }>('/upload/init', data),
  completeUpload: (upload_id: string) => post<FileNode>('/upload/complete', { upload_id }),
  abortUpload: (upload_id: string) => post('/upload/abort', { upload_id }),

  /* ---------------- 回收站 ---------------- */

  listTrash: (params: { space_id?: number; page?: number; page_size?: number }) =>
    get<PageBody<TrashItem>>('/trash', params),
  restoreTrash: (node_ids: number[]) => post<{ restored: number }>('/trash/restore', { node_ids }),
  purgeTrash: (node_ids: number[]) => post<{ purged: number }>('/trash/purge', { node_ids }),

  /* ---------------- 权限 ---------------- */

  listACL: (space_id: number, node_id: number) =>
    get<{ direct: AccessRule[]; inherited: AccessRule[]; catalog: PermissionOption[] }>('/acl', {
      space_id,
      node_id,
    }),
  grant: (data: {
    space_id: number
    node_id: number
    principal_type: PrincipalType
    principal_id?: number
    principal_role?: string
    allow: PermCode[]
    deny?: PermCode[]
    include_sub_dept: boolean
    inheritable: boolean
    expire_days?: number
    remark?: string
  }) => post<AccessRule>('/acl', data),
  revokeACL: (id: number) => del(`/acl/${id}`),
  myPerms: (space_id: number, node_id: number) =>
    get<{ perms: PermCode[]; value: number }>('/acl/mine', { space_id, node_id }),

  /* ---------------- 分享 ---------------- */

  createShare: (data: {
    space_id: number
    node_id: number
    scope: 'internal' | 'public'
    perms: PermCode[]
    password?: string
    expire_days?: number
    max_downloads?: number
    targets?: Array<{ type: PrincipalType; id: number }>
  }) => post<Share>('/shares', data),
  listShares: (params: { page?: number; page_size?: number; mine?: boolean }) =>
    get<PageBody<Share>>('/shares', params),
  revokeShare: (id: number) => del(`/shares/${id}`),
  shareInfo: (code: string, password?: string) =>
    get<{
      share: { code: string; scope: string; perms: PermCode[]; expire_at?: string; downloads: number }
      node: {
        id: number
        name: string
        is_dir: boolean
        size: number
        size_text: string
        ext: string
        mime_type: string
      }
      space_name: string
    }>(`/share/${code}/info`, { password }),
  shareList: (code: string, params: { password?: string; parent_id?: number }) =>
    get<{ items: FileNode[]; parent: FileNode; crumbs: Array<{ id: number; name: string }> }>(
      `/share/${code}/list`,
      params,
    ),

  /* ---------------- Office ---------------- */

  officeConfig: (space_id: number, node_id: number) =>
    get<OfficeEditorConfig>('/office/config', { space_id, node_id }),

  /* ---------------- 通讯录（授权选人用） ---------------- */

  searchUsers: (keyword: string, limit = 20) =>
    get<User[]>('/directory/users', { keyword, limit }),
  departments: () => get<Department[]>('/directory/departments'),

  /* ---------------- 管理后台 ---------------- */

  adminOverview: () => get<Overview>('/admin/overview'),
  adminUsers: (params: {
    keyword?: string
    dept_id?: number
    include_sub?: boolean
    role?: string
    status?: string
    page?: number
    page_size?: number
  }) => get<PageBody<User>>('/admin/users', params),
  createUser: (data: {
    username: string
    password: string
    nickname?: string
    email?: string
    phone?: string
    job_title?: string
    dept_id: number
    role: string
    quota?: number
    remark?: string
    must_change_password?: boolean
  }) => post<User>('/admin/users', data),
  updateUser: (
    id: number,
    data: Partial<{
      nickname: string
      email: string
      phone: string
      job_title: string
      dept_id: number
      role: string
      quota: number
      remark: string
      status: string
    }>,
  ) => put<User>(`/admin/users/${id}`, data),
  deleteUser: (id: number) => del(`/admin/users/${id}`),
  resetPassword: (id: number, new_password: string, must_change_password = true) =>
    post(`/admin/users/${id}/reset-password`, { new_password, must_change_password }),

  deptTree: () => get<DeptNode[]>('/admin/departments/tree'),
  createDept: (data: {
    parent_id: number
    name: string
    code?: string
    sort?: number
    leader_id?: number
    remark?: string
    quota?: number
  }) => post<Department>('/admin/departments', data),
  updateDept: (
    id: number,
    data: Partial<{
      name: string
      code: string
      sort: number
      leader_id: number
      remark: string
      enabled: boolean
      parent_id: number
    }>,
  ) => put<Department>(`/admin/departments/${id}`, data),
  deleteDept: (id: number) => del(`/admin/departments/${id}`),

  adminSpaces: () => get<Space[]>('/admin/spaces'),
  updateSpace: (id: number, data: { name?: string; quota?: number }) =>
    put<Space>(`/admin/spaces/${id}`, data),

  auditLogs: (params: {
    username?: string
    action?: string
    keyword?: string
    success?: string
    page?: number
    page_size?: number
  }) => get<PageBody<AuditLog>>('/admin/audit-logs', params),
  auditActions: () => get<string[]>('/admin/audit-actions'),

  settings: () =>
    get<{
      settings: Record<string, string>
      password_min: number
      chunk_size: number
      max_upload_size: number
      office_enabled: boolean
      permission_catalog: PermissionOption[]
    }>('/admin/settings'),
  updateSettings: (data: Record<string, string>) => put<Record<string, string>>('/admin/settings', data),
}

export type { PageBody }
export * from './types'
export { authedURL, ApiError, getToken, setToken, clearToken } from './request'
