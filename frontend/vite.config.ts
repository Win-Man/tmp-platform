import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 前端构建产物直接输出到后端 embed 目录，实现单二进制交付。
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: '../backend/web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 2000,
    rollupOptions: {
      output: {
        // 只把 echarts 单独拆出（它不依赖 vue，单独成 chunk 安全且能减小首屏体积）。
        // 注意：绝不能把 element-plus 与 vue 拆到不同 chunk——element-plus 初始化时要访问
        // vue 运行时里的变量，跨 chunk 求值顺序会造成
        // "Cannot access 'X' before initialization"（TDZ）导致整页白屏。
        manualChunks(id) {
          if (id.includes('node_modules/echarts') || id.includes('node_modules/zrender')) {
            return 'charts'
          }
          return undefined
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // 本地开发时把 /api 代理到后端，避免跨域配置。
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true },
      '/healthz': { target: 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
})
