import { api, getToken } from '@/api'
import { ApiError } from '@/api/request'
import type { ConflictMode, FileNode } from '@/api/types'

/** 一个上传任务在界面上的状态。 */
export interface UploadTask {
  id: string
  file: File
  name: string
  size: number
  spaceId: number
  parentId: number
  /** 0-100 */
  progress: number
  status: 'queued' | 'hashing' | 'uploading' | 'done' | 'error' | 'canceled'
  message?: string
  instant?: boolean
  /** 撞上同名文件时怎么办；不填按"保留两者"。 */
  conflict?: ConflictMode
  node?: FileNode
  controller?: AbortController
}

/**
 * 同时上传的文件数上限。
 *
 * 浏览器对同一个域名只开 6 条并发连接，多出来的请求在浏览器里排队。
 * 而 axios 的超时是从"创建请求"开始算的，不是从"真正发出去"开始算——
 * 一口气丢几十个文件进去，排在后面的会在还没轮到自己时就超时，
 * 界面上就是一片"网络连接失败"。所以文件层面必须自己限流：
 * 3 个文件 × 每个最多 3 个分片请求 = 9 条，刚好压在浏览器的连接池附近。
 */
export const FILE_CONCURRENCY = 3

/**
 * 判断一个错误值不值得重试。
 *
 * ApiError.code 为 0 表示压根没收到响应（断网、超时、连接被掐）；
 * 5xx 是服务端临时抽风。这两类再试一次往往就过了。
 * 4xx（没权限、配额满、文件名非法）重试多少次都是同一个结果，立刻失败反而干脆。
 */
function isTransient(err: unknown): boolean {
  if (err instanceof ApiError) {
    return err.code === 0 || (err.code >= 500 && err.code < 600) || err.code === 50000
  }
  // fetch 在网络层出错时抛的是 TypeError；主动取消抛的是 AbortError，不在此列。
  return err instanceof TypeError
}

/** 出错就退避重试，只重试 isTransient 认可的那些。 */
async function withRetry<T>(fn: () => Promise<T>, signal: AbortSignal, tries = 3): Promise<T> {
  let last: unknown
  for (let i = 0; i < tries; i += 1) {
    if (signal.aborted) throw new DOMException('已取消', 'AbortError')
    try {
      return await fn()
    } catch (err) {
      last = err
      if (signal.aborted || !isTransient(err) || i === tries - 1) throw err
      // 0.8s、1.6s、3.2s……再加一点随机，免得几个任务同时失败又同时重来。
      const wait = 800 * 2 ** i + Math.random() * 400
      await new Promise((resolve) => setTimeout(resolve, wait))
    }
  }
  throw last
}

/**
 * 哈希闸门：同一时刻只算一个文件的哈希。
 *
 * computeHash 要把整个文件读进内存，几十个文件一起算会直接把标签页撑爆。
 * 排队算还有个额外好处：CPU 不用在一堆任务之间来回切，总耗时反而更短。
 */
let hashGate: Promise<unknown> = Promise.resolve()
function queueHash(file: File): Promise<string> {
  const run = hashGate.then(() => computeHash(file))
  // 失败也要放行下一个，否则整条队列卡死。
  hashGate = run.catch(() => undefined)
  return run
}

/**
 * 计算整文件 SHA-256，用于秒传与完整性校验。
 *
 * crypto.subtle 只在安全上下文（https 或 localhost）下可用。
 * 企业内网常常是裸 http + IP 访问，这时拿不到哈希——返回空串，
 * 上传照常走，只是没有秒传，不影响可用性。
 */
export async function computeHash(file: File, onProgress?: (p: number) => void): Promise<string> {
  if (!globalThis.crypto?.subtle) return ''
  // 大文件一次性读进内存会爆，超过阈值就跳过秒传。
  const MAX_HASH_SIZE = 2 * 1024 * 1024 * 1024
  if (file.size > MAX_HASH_SIZE) return ''
  try {
    onProgress?.(0)
    const buffer = await file.arrayBuffer()
    const digest = await crypto.subtle.digest('SHA-256', buffer)
    onProgress?.(100)
    return Array.from(new Uint8Array(digest))
      .map((b) => b.toString(16).padStart(2, '0'))
      .join('')
  } catch {
    return ''
  }
}

/** 上传单个分片。走原生 fetch 以便用 AbortController 取消。 */
async function putChunk(uploadId: string, index: number, blob: Blob, signal: AbortSignal) {
  const form = new FormData()
  form.append('upload_id', uploadId)
  form.append('index', String(index))
  form.append('chunk', blob)
  const resp = await fetch('/api/v1/upload/chunk', {
    method: 'POST',
    headers: { Authorization: `Bearer ${getToken()}` },
    body: form,
    signal,
  })
  // 包成 ApiError，好让 isTransient 分得清"服务端 500"和"没权限"。
  if (!resp.ok) {
    throw new ApiError(resp.status, `分片 ${index + 1} 上传失败（HTTP ${resp.status}）`)
  }
  const body = await resp.json()
  if (body.code !== 0) {
    throw new ApiError(body.code ?? 0, body.message || `分片 ${index + 1} 上传失败`)
  }
}

/**
 * 执行一个上传任务：秒传 → 分片上传（跳过已传分片）→ 合并。
 *
 * 分片并发固定为 3：再高对单机磁盘收益有限，反而容易把浏览器的连接数占满。
 */
export async function runUpload(task: UploadTask, onTick: () => void): Promise<FileNode | null> {
  const controller = new AbortController()
  task.controller = controller

  task.status = 'hashing'
  task.progress = 0
  onTick()
  const hash = await queueHash(task.file)
  if (controller.signal.aborted) return null

  task.status = 'uploading'
  onTick()

  const init = await withRetry(
    () =>
      api.initUpload({
        space_id: task.spaceId,
        parent_id: task.parentId,
        filename: task.name,
        size: task.size,
        hash,
        conflict: task.conflict,
      }),
    controller.signal,
  )

  if (init.instant && init.node) {
    task.instant = true
    task.progress = 100
    task.status = 'done'
    task.node = init.node
    onTick()
    return init.node
  }

  const uploadId = init.upload_id!
  const chunkSize = init.chunk_size || 8 * 1024 * 1024
  const chunkCount = init.chunk_count || 1
  const done = new Set(init.uploaded || [])

  const pending: number[] = []
  for (let i = 0; i < chunkCount; i += 1) {
    if (!done.has(i)) pending.push(i)
  }
  // 断点续传：已传的分片直接计入进度。
  const updateProgress = () => {
    task.progress = Math.min(99, Math.floor((done.size / chunkCount) * 100))
    onTick()
  }
  updateProgress()

  const CONCURRENCY = 3
  let cursor = 0
  let failure: Error | null = null

  const worker = async () => {
    while (cursor < pending.length && !failure && !controller.signal.aborted) {
      const index = pending[cursor]
      cursor += 1
      const start = index * chunkSize
      const blob = task.file.slice(start, Math.min(start + chunkSize, task.size))
      try {
        // 分片是幂等的（同一个序号写同一个文件），重试最安全也最划算：
        // 一片失败就整个文件重来太亏，何况大文件有上百片。
        await withRetry(() => putChunk(uploadId, index, blob, controller.signal), controller.signal)
        done.add(index)
        updateProgress()
      } catch (err) {
        if (controller.signal.aborted) return
        failure = err instanceof Error ? err : new Error(String(err))
      }
    }
  }

  await Promise.all(Array.from({ length: Math.min(CONCURRENCY, pending.length || 1) }, worker))

  if (controller.signal.aborted) {
    task.status = 'canceled'
    await api.abortUpload(uploadId).catch(() => undefined)
    onTick()
    return null
  }
  if (failure) throw failure

  // 后端记住了这次会话生成的节点，重复调用会把同一个节点还回来，所以敢重试。
  const node = await withRetry(() => api.completeUpload(uploadId), controller.signal)
  task.progress = 100
  task.status = 'done'
  task.node = node
  onTick()
  return node
}

/** 从 DataTransfer 中递归取出所有文件，保留相对路径以便重建目录结构。 */
export async function filesFromDataTransfer(dt: DataTransfer): Promise<Array<{ file: File; path: string }>> {
  const out: Array<{ file: File; path: string }> = []
  const items = Array.from(dt.items || [])
  const entries = items
    .map((item) => (item.webkitGetAsEntry ? item.webkitGetAsEntry() : null))
    .filter(Boolean) as FileSystemEntry[]

  if (!entries.length) {
    Array.from(dt.files || []).forEach((file) => out.push({ file, path: file.name }))
    return out
  }

  const walk = async (entry: FileSystemEntry, prefix: string): Promise<void> => {
    if (entry.isFile) {
      const file = await new Promise<File>((resolve, reject) =>
        (entry as FileSystemFileEntry).file(resolve, reject),
      )
      out.push({ file, path: prefix + entry.name })
      return
    }
    const reader = (entry as FileSystemDirectoryEntry).createReader()
    // readEntries 一次最多返回 100 条，要反复读到空为止。
    const children: FileSystemEntry[] = []
    for (;;) {
      const batch = await new Promise<FileSystemEntry[]>((resolve, reject) =>
        reader.readEntries(resolve, reject),
      )
      if (!batch.length) break
      children.push(...batch)
    }
    for (const child of children) {
      await walk(child, `${prefix}${entry.name}/`)
    }
  }

  for (const entry of entries) {
    await walk(entry, '')
  }
  return out
}
