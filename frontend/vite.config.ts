import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

export default defineConfig({
  plugins: [
    vue(),
    // Element Plus 组件按需引入：只打包页面实际用到的 el-* 组件，
    // 避免全量 app.use(ElementPlus) 把 70+ 组件塞进主包。
    Components({
      resolvers: [ElementPlusResolver()],
      // 不生成 components.d.ts：其全局组件声明会让 el-table 插槽 row 被推断为
      // DefaultRow，与页面里具体的行类型冲突（100+ TS 报错）。运行时按需引入不受影响。
      dts: false,
    }),
  ],
  base: '/promai/',
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8091',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
  },
})
