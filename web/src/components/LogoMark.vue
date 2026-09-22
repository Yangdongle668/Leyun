<script setup lang="ts">
/**
 * 乐云标识。
 *
 * 造型是上下两片云：上层实心、下层半透明。
 * 取"一朵云下面分着层"的意思——这个产品的核心就是
 * 文件放在一片云上，但按部门分层授权。
 *
 * 几个约束：
 *   · 只用 currentColor，放在蓝底上是白的、放在白底上是蓝的，不用维护两套
 *   · 轮廓足够简单，缩到 24px 仍然认得出是云而不是一团东西
 *   · 不含文字，中英文场合都能用
 */
withDefaults(defineProps<{ size?: number }>(), { size: 28 })
</script>

<template>
  <svg
    class="ly-logo-mark"
    :width="size"
    :height="size"
    viewBox="0 0 32 32"
    role="img"
    aria-label="乐云"
  >
    <!-- 下层：淡一档，露出一道边，形成"分层"的暗示 -->
    <g fill="currentColor" opacity=".45">
      <circle cx="12" cy="20" r="5.6" />
      <circle cx="21" cy="20.4" r="5.2" />
      <rect x="6.4" y="19.6" width="20" height="6.2" rx="3.1" />
    </g>
    <!-- 上层：主云形 -->
    <g fill="currentColor">
      <circle cx="11.6" cy="14.6" r="5.4" />
      <circle cx="20.4" cy="15" r="4.9" />
      <circle cx="16" cy="10.4" r="5.8" />
      <rect x="6.2" y="14.2" width="20" height="5.9" rx="2.95" />
    </g>
  </svg>
</template>

<style scoped>
.ly-logo-mark {
  display: block;
  flex-shrink: 0;
}
</style>
