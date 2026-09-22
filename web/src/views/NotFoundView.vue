<script setup lang="ts">
import { useRouter } from 'vue-router'
const router = useRouter()
</script>

<template>
  <div class="ly-404">
    <div class="ly-404-code">404</div>
    <h1>页面不存在</h1>
    <p class="ly-muted">你访问的地址可能已被移动或删除。</p>
    <el-button type="primary" @click="router.push('/files')">返回文件</el-button>
  </div>
</template>

<style scoped>
.ly-404 {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  background: var(--ly-bg);
}
.ly-404-code {
  font-size: 76px;
  font-weight: 700;
  letter-spacing: 4px;
  color: var(--ly-primary);
  opacity: 0.18;
  line-height: 1;
}
.ly-404 h1 {
  margin: 6px 0 0;
  font-size: 20px;
  font-weight: 600;
}
.ly-404 p {
  margin: 0 0 14px;
  font-size: 13px;
}
</style>
