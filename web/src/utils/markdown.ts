import DOMPurify from 'dompurify'
import { marked } from 'marked'

/**
 * 把大模型的回答按 Markdown 渲染成 HTML。
 *
 * 为什么一定要过一遍消毒：
 *
 * 回答是拿云盘里的文档拼出来的，而文档是员工自己上传的——等于用户可控输入。
 * 有人往 docx 里塞一段 <img src=x onerror=...>，模型照抄进回答，
 * 直接 v-html 就是一个能打到所有同事的 XSS。
 *
 * 所以不手写渲染器（正则拼 HTML 迟早漏），而是 marked 解析 + DOMPurify 消毒，
 * 并且只放行排版需要的那几个标签。链接另外处理，见下面的 hook。
 */

// 只留排版用得上的。table 系列保留：规格参数这类回答经常是表格。
const ALLOWED_TAGS = [
  'p', 'br', 'strong', 'em', 'del', 'code', 'pre', 'blockquote',
  'ul', 'ol', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr',
  'table', 'thead', 'tbody', 'tr', 'th', 'td', 'a',
]

// 不放行 style、class 之类：一个 style 就能做出覆盖整页的透明遮罩去骗点击。
const ALLOWED_ATTR = ['href', 'title']

let hooked = false
function ensureHooks() {
  if (hooked) return
  hooked = true
  DOMPurify.addHook('afterSanitizeAttributes', (node) => {
    if (node.tagName !== 'A') return
    const href = node.getAttribute('href') || ''
    // javascript: 和 data: 开头的一律去掉。DOMPurify 默认也拦，
    // 这里再挡一层，免得将来有人放宽配置时顺手把这个也放开了。
    if (!/^(https?:|mailto:|#|\/)/i.test(href)) {
      node.removeAttribute('href')
      return
    }
    // 外链新开窗口。noopener 是必须的：不加的话新页面能通过
    // window.opener 把原页面导航走（钓鱼）。
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  })
}

/** 渲染 Markdown；传进来的永远当作不可信内容处理。 */
export function renderMarkdown(src: string): string {
  if (!src) return ''
  ensureHooks()
  // async: false 让 marked 返回字符串而不是 Promise。
  const raw = marked.parse(src, { async: false, breaks: true, gfm: true }) as string
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS,
    ALLOWED_ATTR,
    // 上面的 hook 要加 target/rel，得允许它们通过。
    ADD_ATTR: ['target', 'rel'],
  })
}
