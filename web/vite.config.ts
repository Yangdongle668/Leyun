import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 构建产物直接落到 Go 的 embed 目录，`go build` 后即为单文件可执行程序。
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        // 把体积大且不常变的依赖拆出来，升级业务代码时浏览器缓存仍然有效。
        // 这里必须用函数形式：对象形式在 rolldown 下不被支持。
        manualChunks(id: string) {
          if (!id.includes('node_modules')) return
          if (id.includes('element-plus') || id.includes('@element-plus')) return 'element-plus'
          if (id.includes('/vue/') || id.includes('vue-router') || id.includes('pinia') || id.includes('axios')) {
            return 'vendor'
          }
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // 开发态把接口透传给本机后端，免去跨域配置。
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
})
