<template>
  <el-config-provider :locale="zhCn">
  <div v-if="showLayout" class="fc-app">
    <!-- 侧栏：v4 白色 196px + 分组导航 -->
    <aside class="fc-side">
      <div class="fc-brand">
        <div class="fc-logo">P</div>
        <h1>PromAI</h1>
      </div>
      <nav class="fc-nav">
        <a :class="{ on: isActive('/dashboard') }" @click="go('/dashboard')"><span class="ic">⌂</span>首页</a>
        <a class="fc-static"><span class="ic">⇢</span>快捷跳转<span class="caret">›</span></a>
        <a class="fc-static"><span class="ic">⇄</span>空间切换<span class="caret">›</span></a>

        <div class="fc-grp">观测</div>
        <a :class="{ on: isActive('/pipeline') }" @click="go('/pipeline')"><span class="ic">⇢</span>告警驱动巡检<span class="caret">›</span></a>
        <!-- FEATURE_FLAG: 北极星 / 灭火图暂不开放（未达 Flashcat 效果），恢复时把 SHOW_FIREMAP_SCENE 改为 true -->
        <template v-if="SHOW_FIREMAP_SCENE">
          <a :class="{ on: isActive('/polaris') }" @click="go('/polaris')"><span class="ic">★</span>北极星<span class="caret">›</span></a>
          <a :class="{ on: route.path.startsWith('/firemap') }" @click="toggle('firemap')"><span class="ic">◉</span>灭火图<span class="caret">⌄</span></a>
          <div class="fc-sub" v-show="open.firemap">
            <a :class="{ on: firemapTab === '' }" @click="go('/firemap')">首页</a>
            <a :class="{ on: firemapTab === 'pool' }" @click="go('/firemap?view=pool')">卡片池</a>
            <a :class="{ on: firemapTab === 'status' }" @click="go('/firemap?view=status')">状态总览</a>
            <a :class="{ on: firemapTab === 'slo' }" @click="go('/firemap?view=slo')">SLO报表</a>
          </div>
        </template>
        <a :class="{ on: isActive('/eventwall') }" @click="go('/eventwall')"><span class="ic">▤</span>事件墙<span class="caret">›</span></a>
        <a :class="{ on: isActive('/bi') }" @click="go('/bi')"><span class="ic">◍</span>健康大屏</a>

        <div class="fc-grp">分析</div>
        <a :class="{ on: isActive('/metrics') }" @click="go('/metrics')"><span class="ic">∿</span>指标分析<span class="caret">›</span></a>
        <a :class="{ on: isActive('/log-analysis') }" @click="go('/log-analysis')"><span class="ic">☰</span>日志分析<span class="caret">›</span></a>
        <a :class="{ on: isActive('/trace-analysis') }" @click="go('/trace-analysis')"><span class="ic">⑂</span>链路分析<span class="caret">›</span></a>

        <div class="fc-grp">平台</div>
        <a :class="{ on: isActive('/dashboard') }" @click="go('/dashboard')"><span class="ic">▦</span>仪表盘<span class="caret">›</span></a>
        <a :class="{ on: open.alert }" @click="toggle('alert')"><span class="ic">⚑</span>告警管理<span class="caret">{{ open.alert ? '⌄' : '›' }}</span></a>
        <div class="fc-sub" v-show="open.alert">
          <a :class="{ on: isActive('/alerts') }" @click="go('/alerts')">实时告警</a>
          <a :class="{ on: isActive('/alert-rules') }" @click="go('/alert-rules')">告警规则</a>
          <a :class="{ on: isActive('/alert-silences') }" @click="go('/alert-silences')">告警静默</a>
          <a :class="{ on: isActive('/alert-inhibits') }" @click="go('/alert-inhibits')">抑制规则</a>
          <a :class="{ on: isActive('/alert-expectations') }" @click="go('/alert-expectations')">预期行为</a>
          <a :class="{ on: isActive('/alert-routes') }" @click="go('/alert-routes')">通知路由</a>
          <a :class="{ on: isActive('/alert-sources') }" @click="go('/alert-sources')">告警源管理</a>
          <a :class="{ on: isActive('/alert-history') }" @click="go('/alert-history')">告警历史</a>
          <a :class="{ on: isActive('/alert-events') }" @click="go('/alert-events')">事件聚合</a>
        </div>
        <a :class="{ on: open.tool }" @click="toggle('tool')"><span class="ic">⚙</span>监控工具<span class="caret">{{ open.tool ? '⌄' : '›' }}</span></a>
        <div class="fc-sub" v-show="open.tool">
          <a :class="{ on: isActive('/datasources') }" @click="go('/datasources')">数据源管理</a>
          <a :class="{ on: isActive('/notifications') }" @click="go('/notifications')">通知渠道</a>
          <a :class="{ on: isActive('/cronjobs') }" @click="go('/cronjobs')">定时任务</a>
          <a :class="{ on: isActive('/reports') }" @click="go('/reports')">报告管理</a>
          <a :class="{ on: isActive('/templates') }" @click="go('/templates')">巡检模板</a>
          <a :class="{ on: isActive('/inspection') }" @click="go('/inspection')">触发巡检</a>
          <a :class="{ on: isActive('/inspect-records') }" @click="go('/inspect-records')">巡检记录</a>
          <a :class="{ on: isActive('/skills') }" @click="go('/skills')">Skill 管理</a>
          <a :class="{ on: isActive('/ai') }" @click="go('/ai')">AI 助手</a>
          <a :class="{ on: isActive('/portscan') }" @click="go('/portscan')">敏感端口检测</a>
        </div>
        <a :class="{ on: open.k8s }" @click="toggle('k8s')"><span class="ic">⎈</span>容器平台<span class="caret">{{ open.k8s ? '⌄' : '›' }}</span></a>
        <div class="fc-sub" v-show="open.k8s">
          <a :class="{ on: isActive('/user-manual') }" @click="go('/user-manual')">使用手册</a>
          <a :class="{ on: isActive('/settings') }" @click="go('/settings')">系统设置</a>
        </div>
      </nav>
      <div class="fc-side-ft">
        <span class="fc-avatar-sm">AD</span>
        <span>{{ username }}</span>
        <span class="fc-ft-sub">PromAI 内核</span>
      </div>
    </aside>

    <!-- 主区：50px 顶栏 + 内容 -->
    <div class="fc-main">
      <header class="fc-top">
        <span class="fc-top-title">{{ currentTitle }}</span>
        <span class="fc-space">◈ 默认空间 ⌄</span>
        <span v-if="SHOW_FIREMAP_SCENE" class="fc-what">? 什么是灭火图</span>
        <span class="fc-spacer"></span>
        <button class="fc-ai-btn" @click="go('/ai')">✦ PromAI</button>
        <a class="fc-top-link" @click="go('/user-manual')">创建引导</a>
        <a class="fc-top-link" @click="go('/user-manual')">产品文档</a>
        <el-dropdown trigger="click" @command="handleThemeChange">
          <span class="fc-avatar">AD</span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item v-for="t in themeOptions" :key="t.value" :command="t.value">
                {{ t.icon }} {{ t.label }}
              </el-dropdown-item>
              <el-dropdown-item command="__logout__" divided>退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </header>
      <main class="fc-content">
        <router-view v-slot="{ Component }">
          <transition name="fc-fade" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </main>
    </div>
  </div>
  <router-view v-else />
  </el-config-provider>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import { useTheme } from './composables/useTheme'

const route = useRoute()
const router = useRouter()
const showLayout = computed(() => route.path !== '/login')
// FEATURE_FLAG: 北极星 / 灭火图暂不开放；恢复时改为 true 即可重新显示导航。
const SHOW_FIREMAP_SCENE = false
const currentTitle = computed(() => (route.meta.title as string) || 'PromAI')
const username = ref(localStorage.getItem('username') || 'Admin')
const { setTheme, themeOptions } = useTheme()

type GroupKey = 'firemap' | 'alert' | 'tool' | 'k8s'
const open = reactive<Record<GroupKey, boolean>>({ firemap: true, alert: false, tool: false, k8s: false })
const firemapTab = computed(() => (route.query.view as string) || '')

watch(
  () => route.path,
  (p) => {
    if (p.startsWith('/firemap')) open.firemap = true
    if (p.startsWith('/alert')) open.alert = true
  },
  { immediate: true },
)

function go(path: string) {
  if (route.fullPath !== path) router.push(path)
}
function isActive(path: string) {
  return route.path === path
}
function toggle(k: GroupKey) {
  open[k] = !open[k]
}
function handleThemeChange(cmd: string) {
  if (cmd === '__logout__') {
    localStorage.removeItem('token')
    localStorage.removeItem('username')
    router.push('/login')
    return
  }
  setTheme(cmd as any)
}
</script>

<style scoped>
/* 全局 body 为 overflow:hidden，因此整站滚动必须由 .fc-content 内部承担。
   .fc-app 固定视口高度且禁止溢出，否则内容撑开后无处可滚。 */
.fc-app { display: flex; height: 100vh; overflow: hidden; background: var(--bg-primary); color: var(--text-primary); }

/* ===== 侧栏（v4：白色 196px） ===== */
.fc-side {
  width: var(--sidebar-width); background: var(--bg-secondary);
  border-right: 1px solid var(--border); flex-shrink: 0;
  display: flex; flex-direction: column; flex-shrink: 0;
  height: 100vh; overflow-y: auto; z-index: 6;
}
.fc-brand { padding: 13px 15px; display: flex; gap: 8px; align-items: center; border-bottom: 1px solid #f0f1f3; }
.fc-logo {
  width: 26px; height: 26px; border-radius: 7px;
  background: linear-gradient(135deg, #8b6ee0, #5b3fb0);
  display: flex; align-items: center; justify-content: center;
  color: #fff; font-weight: 700; font-size: 14px;
}
.fc-brand h1 { margin: 0; font-size: 14px; font-weight: 600; color: #2b2f36; letter-spacing: .3px; }
.fc-nav { flex: 1; padding: 6px 0; }
.fc-nav a {
  display: flex; align-items: center; gap: 9px; padding: 7px 16px;
  color: #4a5058; text-decoration: none; font-size: 12px; cursor: pointer;
  transition: .12s; border-left: 3px solid transparent; user-select: none;
}
.fc-nav a:hover { background: #f7f7fa; }
.fc-nav a.on { background: var(--cyan-dim); color: var(--cyan); border-left-color: var(--cyan); font-weight: 600; }
.fc-nav a .ic { width: 15px; text-align: center; color: #8f96a0; font-size: 13px; }
.fc-nav a.on .ic { color: var(--cyan); }
.fc-nav a .caret { margin-left: auto; font-size: 9px; color: #b0b6bf; }
.fc-nav a.fc-static { cursor: default; }
.fc-nav a.fc-static:hover { background: transparent; }
.fc-grp { padding: 13px 16px 4px; font-size: 10px; color: #b0b6bf; letter-spacing: 1px; }
.fc-sub a { padding: 5px 16px 5px 40px; font-size: 11.5px; color: #7a818b; }
.fc-sub a.on { background: var(--cyan-dim); color: var(--cyan); font-weight: 600; }
.fc-side-ft {
  padding: 10px 16px; border-top: 1px solid #f0f1f3; font-size: 10.5px;
  color: #9aa1ac; display: flex; align-items: center; gap: 6px;
}
.fc-avatar-sm {
  width: 18px; height: 18px; border-radius: 50%; flex-shrink: 0;
  background: linear-gradient(135deg, #7c5cd6, #4a3290); color: #fff;
  display: flex; align-items: center; justify-content: center; font-size: 8px;
}
.fc-ft-sub { margin-left: auto; font-size: 9px; color: #c2c8d0; }

/* ===== 主区 ===== */
/* 主区固定视口高度，overflow:hidden 让滚动只发生在 .fc-content 内。 */
.fc-main { flex: 1; min-width: 0; height: 100vh; overflow: hidden; display: flex; flex-direction: column; }
.fc-top {
  height: var(--header-height); background: #fff; border-bottom: 1px solid var(--border);
  display: flex; align-items: center; padding: 0 18px; gap: 12px;
  position: sticky; top: 0; z-index: 5; flex-shrink: 0;
}
.fc-top-title { font-size: 15px; font-weight: 600; color: #2b2f36; }
.fc-space {
  display: flex; align-items: center; gap: 5px; font-size: 11.5px; color: var(--text-secondary);
  border: 1px solid var(--border); border-radius: 5px; padding: 4px 8px; cursor: pointer;
}
.fc-what { font-size: 11.5px; color: var(--text-tertiary); cursor: pointer; }
.fc-spacer { flex: 1; }
.fc-ai-btn {
  display: flex; align-items: center; gap: 6px; background: var(--cyan); color: #fff;
  border: none; border-radius: 6px; padding: 6px 13px; font-size: 11.5px;
  font-weight: 600; cursor: pointer; box-shadow: 0 1px 4px rgba(109, 75, 196, .25);
}
.fc-ai-btn:hover { background: var(--primary-hover); }
.fc-top-link { font-size: 11.5px; color: var(--text-secondary); cursor: pointer; }
.fc-top-link:hover { color: var(--cyan); }
.fc-avatar {
  width: 26px; height: 26px; border-radius: 50%; cursor: pointer;
  background: linear-gradient(135deg, #7c5cd6, #4a3290); color: #fff;
  display: flex; align-items: center; justify-content: center; font-size: 10px;
}
.fc-content { flex: 1; min-height: 0; overflow: auto; position: relative; }

.fc-fade-enter-active, .fc-fade-leave-active { transition: opacity .2s ease, transform .2s ease; }
.fc-fade-enter-from { opacity: 0; transform: translateY(5px); }
.fc-fade-leave-to { opacity: 0; transform: translateY(-5px); }
</style>
