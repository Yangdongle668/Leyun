import { api, getToken } from '@/api'
import type { FileNode } from '@/api/types'

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
  status: 'hashing' | 'uploading' | 'done' | 'error' | 'canceled'
  message?: string
  instant?: boolean
  node?: FileNode
  controller?: AbortController
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
  if (!resp.ok) throw new Error(`分片 ${index + 1} 上传失败（HTTP ${resp.status}）`)
  const body = await resp.json()
  if (body.code !== 0) throw new Error(body.message || `分片 ${index + 1} 上传失败`)
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
  const hash = await computeHash(task.file)
  if (controller.signal.aborted) return null

  task.status = 'uploading'
  onTick()

  const init = await api.initUpload({
    space_id: task.spaceId,
    parent_id: task.parentId,
    filename: task.name,
    size: task.size,
    hash,
  })

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
        await putChunk(uploadId, index, blob, controller.signal)
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

  const node = await api.completeUpload(uploadId)
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
