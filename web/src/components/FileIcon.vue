<script setup lang="ts">
import { computed } from 'vue'
import { extOf } from '@/utils/format'

const props = defineProps<{ name: string; isDir: boolean; size?: number }>()

/**
 * 按扩展名分类上色。
 *
 * 刻意不引第三方图标包：用一个带颜色的圆角方块 + 扩展名文字，
 * 既能一眼分辨类型，又不会让列表变花。
 */
const kind = computed(() => {
  if (props.isDir) return { label: '', color: '#f5a524', bg: '#fff5e3', icon: true }
  const ext = extOf(props.name)
  const groups: Array<{ exts: string[]; color: string; bg: string }> = [
    { exts: ['doc', 'docx', 'odt', 'rtf'], color: '#2b579a', bg: '#e8eefa' },
    { exts: ['xls', 'xlsx', 'ods', 'csv'], color: '#1d7044', bg: '#e6f5ec' },
    { exts: ['ppt', 'pptx', 'odp'], color: '#c0430f', bg: '#fdeee6' },
    { exts: ['pdf'], color: '#c8322b', bg: '#fdeceb' },
    { exts: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg'], color: '#7c3aed', bg: '#f2ecfe' },
    { exts: ['mp4', 'webm', 'mov', 'avi', 'mkv'], color: '#0e7490', bg: '#e3f4f8' },
    { exts: ['mp3', 'wav', 'flac', 'm4a', 'ogg'], color: '#b45309', bg: '#fdf1e0' },
    { exts: ['zip', 'rar', '7z', 'tar', 'gz'], color: '#6b7280', bg: '#f1f3f6' },
    { exts: ['txt', 'md', 'log'], color: '#475569', bg: '#eef1f5' },
    { exts: ['go', 'js', 'ts', 'py', 'java', 'c', 'cpp', 'sh', 'json', 'xml', 'yml', 'yaml', 'sql'], color: '#0f766e', bg: '#e4f3f1' },
  ]
  const hit = groups.find((g) => g.exts.includes(ext))
  return {
    label: (ext || 'file').slice(0, 4).toUpperCase(),
    color: hit?.color ?? '#8a93a5',
    bg: hit?.bg ?? '#f1f3f6',
    icon: false,
  }
})

const boxSize = computed(() => props.size ?? 34)
</script>

<template>
  <span
    class="ly-file-icon"
    :style="{
      width: `${boxSize}px`,
      height: `${boxSize}px`,
      background: kind.bg,
      color: kind.color,
      fontSize: `${Math.max(9, boxSize * 0.29)}px`,
    }"
  >
    <el-icon v-if="kind.icon" :size="boxSize * 0.56"><Folder /></el-icon>
    <template v-else>{{ kind.label }}</template>
  </span>
</template>

<style scoped>
.ly-file-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  font-weight: 600;
  letter-spacing: 0.2px;
  flex-shrink: 0;
  user-select: none;
}
</style>
