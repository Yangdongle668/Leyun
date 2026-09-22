import { getToken } from './request'
import type { KBCitation } from './types'

/** 后端推过来的一帧。 */
export type KBEvent =
  | { type: 'status'; data: string }
  | { type: 'citations'; data: KBCitation[] }
  | { type: 'delta'; data: string }
  | { type: 'done'; data: { conv_id: number; answer: string } }
  | { type: 'error'; data: string }

export interface AskHandlers {
  onStatus?: (text: string) => void
  onCitations?: (items: KBCitation[]) => void
  onDelta?: (text: string) => void
  onDone?: (convID: number, answer: string) => void
  onError?: (message: string) => void
}

/**
 * 发起一次提问并逐帧接收回答。
 *
 * 用 fetch 而不是 EventSource：EventSource 只能发 GET，也没法带
 * Authorization 头，把令牌塞进查询串又会落进各级访问日志。
 *
 * 返回一个中止函数，用户切走或点"停止"时调用。
 */
export function askKB(
  body: { question: string; conv_id?: number },
  handlers: AskHandlers,
): () => void {
  const controller = new AbortController()

  void (async () => {
    try {
      const resp = await fetch('/api/v1/kb/ask', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${getToken()}`,
        },
        body: JSON.stringify(body),
        signal: controller.signal,
      })

      if (!resp.ok || !resp.body) {
        // SSE 正常情况下状态码永远是 200，错误在帧里传。
        // 走到这里说明请求根本没被受理（未登录、被网关拦住之类）。
        handlers.onError?.(`请求失败（HTTP ${resp.status}）`)
        return
      }

      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })

        // SSE 以空行分帧。网络分片可能把一帧切断，所以留最后一段不完整的在缓冲里。
        const frames = buffer.split('\n\n')
        buffer = frames.pop() ?? ''
        for (const frame of frames) {
          dispatch(frame, handlers)
        }
      }
      if (buffer.trim()) dispatch(buffer, handlers)
    } catch (err) {
      if ((err as Error)?.name === 'AbortError') return
      handlers.onError?.((err as Error)?.message || '连接中断')
    }
  })()

  return () => controller.abort()
}

function dispatch(frame: string, handlers: AskHandlers) {
  for (const line of frame.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed.startsWith('data:')) continue
    const payload = trimmed.slice(5).trim()
    if (!payload) continue

    let ev: KBEvent
    try {
      ev = JSON.parse(payload) as KBEvent
    } catch {
      continue
    }
    switch (ev.type) {
      case 'status':
        handlers.onStatus?.(ev.data)
        break
      case 'citations':
        handlers.onCitations?.(ev.data ?? [])
        break
      case 'delta':
        handlers.onDelta?.(ev.data)
        break
      case 'done':
        handlers.onDone?.(ev.data.conv_id, ev.data.answer)
        break
      case 'error':
        handlers.onError?.(ev.data)
        break
    }
  }
}
