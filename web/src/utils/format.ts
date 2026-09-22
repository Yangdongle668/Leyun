import type { PermCode, SpaceType } from '@/api/types'

/** 把字节数格式化成便于阅读的字符串。 */
export function humanSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let n = bytes
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i += 1
  }
  return `${i === 0 ? n : n.toFixed(1)} ${units[i]}`
}

/** 把 GB 数转成字节，供配额输入框使用。 */
export function gbToBytes(gb: number): number {
  return Math.max(0, Math.round(gb * 1024 * 1024 * 1024))
}

/** 把字节转成 GB，保留一位小数。 */
export function bytesToGB(bytes: number): number {
  if (!bytes) return 0
  return Math.round((bytes / 1024 / 1024 / 1024) * 10) / 10
}

/** 格式化时间戳，空值返回占位符。 */
export function formatTime(value?: string | null, withSeconds = true): string {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  const pad = (n: number) => String(n).padStart(2, '0')
  const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  const time = withSeconds
    ? `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
    : `${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `${date} ${time}`
}

/** 相对时间，列表里比绝对时间更好扫读。 */
export function relativeTime(value?: string | null): string {
  if (!value) return '—'
  const d = new Date(value).getTime()
  if (Number.isNaN(d)) return '—'
  const diff = Date.now() - d
  const min = 60_000
  const hour = 60 * min
  const day = 24 * hour
  if (diff < min) return '刚刚'
  if (diff < hour) return `${Math.floor(diff / min)} 分钟前`
  if (diff < day) return `${Math.floor(diff / hour)} 小时前`
  if (diff < 30 * day) return `${Math.floor(diff / day)} 天前`
  return formatTime(value, false)
}

const permLabels: Record<PermCode, string> = {
  view: '查看',
  download: '下载',
  upload: '上传',
  edit: '编辑',
  delete: '删除',
  share: '分享',
  manage: '授权管理',
}

/** 权限码转中文。 */
export function permLabel(code: PermCode): string {
  return permLabels[code] ?? code
}

/** 权限码数组转中文串。 */
export function permLabels2Text(codes: PermCode[] = []): string {
  if (!codes.length) return '无权限'
  return codes.map(permLabel).join(' · ')
}

const spaceTypeLabels: Record<SpaceType, string> = {
  personal: '个人空间',
  department: '部门空间',
  public: '公共空间',
}

/** 空间类型转中文。 */
export function spaceTypeLabel(type: SpaceType): string {
  return spaceTypeLabels[type] ?? type
}

const actionLabels: Record<string, string> = {
  'auth.login': '登录',
  'auth.login_failed': '登录失败',
  'auth.logout': '登出',
  'auth.change_password': '修改口令',
  'user.create': '开通账号',
  'user.update': '修改账号',
  'user.delete': '删除账号',
  'user.reset_password': '重置口令',
  'user.status': '变更账号状态',
  'dept.create': '新建部门',
  'dept.update': '修改部门',
  'dept.delete': '删除部门',
  'file.upload': '上传文件',
  'file.download': '下载文件',
  'file.preview': '预览文件',
  'file.mkdir': '新建目录',
  'file.rename': '重命名',
  'file.move': '移动',
  'file.copy': '复制',
  'file.trash': '删除到回收站',
  'file.restore': '从回收站还原',
  'file.purge': '彻底删除',
  'acl.grant': '授予权限',
  'acl.revoke': '撤销权限',
  'share.create': '创建分享',
  'share.revoke': '撤销分享',
  'share.access': '访问分享',
  'setting.update': '修改系统设置',
  'space.update': '修改空间',
}

/** 审计动作码转中文。 */
export function actionLabel(action: string): string {
  return actionLabels[action] ?? action
}

/** 取文件扩展名（小写，不含点）。 */
export function extOf(name: string): string {
  const idx = name.lastIndexOf('.')
  if (idx <= 0) return ''
  return name.slice(idx + 1).toLowerCase()
}
