/** 与后端一一对应的数据结构定义。 */

export type Role = 'super_admin' | 'dept_admin' | 'member'
export type UserStatus = 'active' | 'disabled'
export type SpaceType = 'personal' | 'department' | 'public'
export type PrincipalType = 'user' | 'dept' | 'role' | 'everyone'
export type PermCode = 'view' | 'download' | 'upload' | 'edit' | 'delete' | 'share' | 'manage'

export interface User {
  id: number
  username: string
  nickname: string
  email: string
  phone: string
  job_title: string
  dept_id: number
  role: Role
  status: UserStatus
  quota_bytes: number
  must_change_password: boolean
  last_login_at?: string
  last_login_ip?: string
  created_by: number
  remark: string
  created_at: string
  // 后端附加的展示字段
  role_label: string
  dept_name: string
  dept_path: string
  space_id?: number
  used_bytes: number
  creator_name?: string
  status_label: string
  quota_display: string
}

export interface Department {
  id: number
  parent_id: number
  name: string
  code: string
  path: string
  depth: number
  sort: number
  leader_id: number
  remark: string
  enabled: boolean
  created_at: string
}

export interface DeptNode extends Department {
  user_count: number
  space_id?: number
  children: DeptNode[]
}

export interface Space {
  id: number
  type: SpaceType
  name: string
  owner_id: number
  dept_id: number
  quota_bytes: number
  used_bytes: number
  enabled: boolean
  perms: PermCode[]
  perm_value: number
  dept_name?: string
  owner_name?: string
}

export interface FileNode {
  id: number
  space_id: number
  parent_id: number
  name: string
  is_dir: boolean
  path: string
  size: number
  blob_hash?: string
  mime_type?: string
  ext?: string
  version: number
  trashed: boolean
  created_by: number
  created_at: string
  updated_at: string
  perms: PermCode[]
  perm_value: number
  size_text: string
  creator_name?: string
  editable: boolean
  previewable: boolean
}

export interface Crumb {
  id: number
  name: string
}

export interface ListResult {
  space: Space
  parent: FileNode | null
  crumbs: Crumb[]
  items: FileNode[]
  parent_perms: PermCode[]
  total: number
}

export interface TrashItem extends FileNode {
  space_name: string
  trashed_at?: string
}

export interface AccessRule {
  id: number
  space_id: number
  node_id: number
  principal_type: PrincipalType
  principal_id: number
  principal_role?: Role
  allow: number
  deny: number
  include_sub_dept: boolean
  inheritable: boolean
  expire_at?: string
  remark?: string
  principal_name: string
  allow_codes: PermCode[]
  deny_codes: PermCode[]
  inherited: boolean
}

export interface PermissionOption {
  code: PermCode
  label: string
  value: number
}

export interface Share {
  id: number
  code: string
  space_id: number
  node_id: number
  scope: 'internal' | 'public'
  has_password: boolean
  perms: number
  expire_at?: string
  max_downloads: number
  downloads: number
  views: number
  revoked: boolean
  created_at: string
  node_name: string
  is_dir: boolean
  space_name: string
  perm_codes: PermCode[]
  expired: boolean
  creator?: string
}

export interface AuditLog {
  id: number
  user_id: number
  username: string
  dept_id: number
  action: string
  target_type: string
  target_id: number
  target: string
  detail: string
  success: boolean
  ip: string
  user_agent: string
  created_at: string
}

export interface Overview {
  user_total: number
  user_active: number
  dept_total: number
  space_total: number
  file_total: number
  folder_total: number
  trash_total: number
  share_total: number
  stored_bytes: number
  stored_text: string
  logical_bytes: number
  logical_text: string
  dedup_saved: number
  dedup_saved_text: string
  recent_uploads: number
  top_spaces: Array<{
    id: number
    name: string
    type: SpaceType
    used: number
    used_text: string
    quota: number
  }>
  dept_usage: Array<{
    dept_id: number
    name: string
    members: number
    used: number
    used_text: string
  }>
}

export interface Profile {
  user: User
  spaces: Space[]
  permissions: PermissionOption[]
  is_super_admin: boolean
  must_reset_password: boolean
  office_enabled: boolean
  settings: Record<string, string>
}

export interface OfficeEditorConfig {
  server_url: string
  config: Record<string, unknown>
  mode: 'edit' | 'view'
  file_name: string
}
