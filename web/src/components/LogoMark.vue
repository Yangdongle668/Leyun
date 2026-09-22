<script setup lang="ts">
/**
 * 乐云标识。
 *
 * 造型是一朵云 + 云下一道横杠（半透明）。
 * 取"一朵云，下面托着一层"的意思——这个产品的核心就是
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
    <!-- 云体：三个圆 + 一条底边拼出来，比手写路径好调比例 -->
    <g fill="currentColor">
      <circle cx="11.8" cy="15.4" r="5.8" />
      <circle cx="20.6" cy="15.9" r="5.2" />
      <circle cx="16" cy="10.6" r="6.2" />
      <rect x="6" y="15" width="20.6" height="6.3" rx="3.15" />
    </g>
    <!-- 云下的一道层：淡一档，缩窄一些，读作"承托"而不是第二朵云 -->
    <rect x="9.6" y="23.6" width="13" height="3.4" rx="1.7" fill="currentColor" opacity=".55" />
  </svg>
</template>

<style scoped>
.ly-logo-mark {
  display: block;
  flex-shrink: 0;
}
</style>
