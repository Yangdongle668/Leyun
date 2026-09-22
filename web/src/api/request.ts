import axios, { type AxiosInstance, type AxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'

/** 后端统一返回体。 */
export interface ApiBody<T = unknown> {
  code: number
  message: string
  data: T
}

/** 分页返回体。 */
export interface PageBody<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}

const TOKEN_KEY = 'leyun_token'

/** 读取本地保存的令牌。 */
export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

/** 保存令牌。 */
export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token)
}

/** 清除令牌。 */
export function clearToken() {
  localStorage.removeItem(TOKEN_KEY)
}

const http: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  timeout: 60_000,
})

http.interceptors.request.use((config) => {
  const token = getToken()
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

/** 业务错误：携带后端返回的 code，调用方可据此分支处理。 */
export class ApiError extends Error {
  code: number
  constructor(code: number, message: string) {
    super(message)
    this.code = code
    this.name = 'ApiError'
  }
}

/** 静默处理的错误码：调用方自行决定如何呈现，不弹全局提示。 */
const SILENT_CODES = new Set([42901])

let redirecting = false

http.interceptors.response.use(
  (resp) => resp,
  (error) => {
    // 网络层错误（断网、超时、502 等）在这里统一兜底。
    const status = error?.response?.status
    const body = error?.response?.data as ApiBody | undefined
    const message = body?.message || (status ? `请求失败（HTTP ${status}）` : '网络连接失败，请检查网络')
    if (status === 401) {
      handleUnauthorized()
    } else {
      ElMessage.error(message)
    }
    return Promise.reject(new ApiError(body?.code ?? status ?? 0, message))
  },
)

function handleUnauthorized() {
  clearToken()
  if (redirecting) return
  redirecting = true
  ElMessage.error('登录已过期，请重新登录')
  const next = encodeURIComponent(window.location.pathname + window.location.search)
  // 分享页允许匿名访问，不该被踢回登录页。
  if (window.location.pathname.startsWith('/s/')) {
    redirecting = false
    return
  }
  window.location.href = `/login?redirect=${next}`
}

/** 发起请求并拆包 data；业务码非 0 时抛出 ApiError。 */
export async function request<T = unknown>(config: AxiosRequestConfig): Promise<T> {
  const resp = await http.request<ApiBody<T>>(config)
  const body = resp.data
  if (body.code !== 0) {
    if (body.code === 401 || body.code === 40100) {
      handleUnauthorized()
    } else if (!SILENT_CODES.has(body.code)) {
      ElMessage.error(body.message || '操作失败')
    }
    throw new ApiError(body.code, body.message || '操作失败')
  }
  return body.data
}

export const get = <T = unknown>(url: string, params?: unknown) =>
  request<T>({ url, method: 'get', params })

export const post = <T = unknown>(url: string, data?: unknown, config?: AxiosRequestConfig) =>
  request<T>({ url, method: 'post', data, ...config })

export const put = <T = unknown>(url: string, data?: unknown) =>
  request<T>({ url, method: 'put', data })

export const del = <T = unknown>(url: string, params?: unknown) =>
  request<T>({ url, method: 'delete', params })

/**
 * 拼出带令牌的直链。
 *
 * 下载、预览由 <a>/<img>/<video> 直接发起，带不上自定义请求头，
 * 后端因此也接受 ?token= 形式。
 */
export function authedURL(path: string, params: Record<string, string | number | undefined> = {}) {
  const url = new URL(`/api/v1${path}`, window.location.origin)
  Object.entries(params).forEach(([k, v]) => {
    if (v !== undefined && v !== null && v !== '') url.searchParams.set(k, String(v))
  })
  const token = getToken()
  if (token) url.searchParams.set('token', token)
  return url.toString()
}

export default http
