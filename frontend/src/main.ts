import { createApp } from 'vue'
import 'element-plus/dist/index.css'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'
import App from './App.vue'
import { installStarSightVue } from './starsight'
import router from './router'
import './style.css'

const app = createApp(App)
app.use(router)

// 图标保留全局注册：页面大量使用 <component :is="'Connection'"> 这类动态图标，
// 按需解析无法覆盖字符串形式的动态引用，故图标仍全量注册（体积远小于组件）。
for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

installStarSightVue(app, router)

app.mount('#app')
