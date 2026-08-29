import { ref, watch, reactive, computed } from 'vue'

export type ThemeName = 'dark' | 'light' | 'cyber' | 'minimal' | 'custom'

interface ThemeOption {
  value: ThemeName
  label: string
  icon: string
  desc: string
  builtin: boolean
}

export const themeOptions: ThemeOption[] = [
  { value: 'dark', label: '深邃暗色', icon: '🌙', desc: '深色背景，护眼低功耗', builtin: true },
  { value: 'light', label: '明亮浅色', icon: '☀️', desc: '浅色背景，清晰明亮', builtin: true },
  { value: 'cyber', label: '赛博霓虹', icon: '💠', desc: '霓虹高饱和，赛博朋克风格', builtin: true },
  { value: 'minimal', label: '极简白', icon: '⬜', desc: '纯白极简，干净利落', builtin: true },
  { value: 'custom', label: '自定义', icon: '🎨', desc: '基于主色自动生成专属主题', builtin: false },
]

const STORAGE_KEY = 'promai_theme'
const CUSTOM_KEY = 'promai_custom_theme'

/** 自定义主题变量清单：键名 = CSS 变量名（不含 --） */
export interface CustomTheme {
  bgPrimary: string
  bgCard: string
  bgElevated: string
  textPrimary: string
  textSecondary: string
  border: string
  cyan: string
  red: string
  amber: string
  emerald: string
  purple: string
  /** 预设名称，用于列表展示 */
  name: string
}

export const DEFAULT_CUSTOM_THEME: CustomTheme = {
  name: '我的主题',
  bgPrimary: '#0f172a',
  bgCard: '#1e293b',
  bgElevated: '#334155',
  textPrimary: '#f1f5f9',
  textSecondary: '#94a3b8',
  border: '#475569',
  cyan: '#06b6d4',
  red: '#ef4444',
  amber: '#f59e0b',
  emerald: '#10b981',
  purple: '#a855f7',
}

/** HEX -> rgba 字符串 */
export function hexToRgba(hex: string, alpha: number): string {
  const h = hex.replace('#', '')
  const r = parseInt(h.slice(0, 2), 16)
  const g = parseInt(h.slice(2, 4), 16)
  const b = parseInt(h.slice(4, 6), 16)
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}

function loadTheme(): ThemeName {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved && ['dark', 'light', 'cyber', 'minimal', 'custom'].includes(saved)) {
      return saved as ThemeName
    }
  } catch {}
  return 'dark'
}

function loadCustom(): CustomTheme {
  try {
    const raw = localStorage.getItem(CUSTOM_KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      return { ...DEFAULT_CUSTOM_THEME, ...parsed }
    }
  } catch {}
  return { ...DEFAULT_CUSTOM_THEME }
}

function applyBuiltin(name: ThemeName) {
  document.documentElement.setAttribute('data-theme', name)
  // 切到内置主题时清掉 inline style，覆盖可能残留的 custom 变量
  const root = document.documentElement
  const cssVars = [
    'bg-primary', 'bg-card', 'bg-elevated', 'text-primary', 'text-secondary',
    'border', 'cyan', 'cyan-dim', 'red', 'red-dim', 'amber', 'amber-dim',
    'emerald', 'emerald-dim', 'purple', 'purple-dim',
  ]
  for (const v of cssVars) root.style.removeProperty(`--${v}`)
}

function applyCustom(t: CustomTheme) {
  document.documentElement.setAttribute('data-theme', 'custom')
  const root = document.documentElement
  root.style.setProperty('--bg-primary', t.bgPrimary)
  root.style.setProperty('--bg-secondary', t.bgPrimary)
  root.style.setProperty('--bg-card', t.bgCard)
  root.style.setProperty('--bg-card-hover', t.bgElevated)
  root.style.setProperty('--bg-elevated', t.bgElevated)
  root.style.setProperty('--text-primary', t.textPrimary)
  root.style.setProperty('--text-secondary', t.textSecondary)
  root.style.setProperty('--text-tertiary', t.textSecondary)
  root.style.setProperty('--border', hexToRgba(t.cyan, 0.18))
  root.style.setProperty('--border-glow', hexToRgba(t.cyan, 0.4))
  root.style.setProperty('--cyan', t.cyan)
  root.style.setProperty('--cyan-dim', hexToRgba(t.cyan, 0.1))
  root.style.setProperty('--red', t.red)
  root.style.setProperty('--red-dim', hexToRgba(t.red, 0.1))
  root.style.setProperty('--amber', t.amber)
  root.style.setProperty('--amber-dim', hexToRgba(t.amber, 0.1))
  root.style.setProperty('--emerald', t.emerald)
  root.style.setProperty('--emerald-dim', hexToRgba(t.emerald, 0.1))
  root.style.setProperty('--purple', t.purple)
  root.style.setProperty('--purple-dim', hexToRgba(t.purple, 0.1))
  root.style.setProperty('--blue', t.cyan)
  // 自定义主题以背景亮度推断 text-inverse
  const dark = isDarkColor(t.bgPrimary)
  root.style.setProperty('--text-inverse', dark ? t.bgPrimary : '#ffffff')
  root.style.setProperty('--primary-hover', t.cyan)
  root.style.setProperty('--primary-active', t.cyan)
  root.style.setProperty('--shadow-glow', `0 0 20px ${hexToRgba(t.cyan, 0.12)}`)
  root.style.setProperty('--shadow-card', dark
    ? '0 4px 24px rgba(0, 0, 0, 0.4)'
    : '0 4px 24px rgba(0, 0, 0, 0.08)')
  root.style.setProperty('--scrollbar-thumb', hexToRgba(t.cyan, 0.3))
  root.style.setProperty('--scrollbar-thumb-hover', hexToRgba(t.cyan, 0.5))
}

function isDarkColor(hex: string): boolean {
  const h = hex.replace('#', '')
  const r = parseInt(h.slice(0, 2), 16)
  const g = parseInt(h.slice(2, 4), 16)
  const b = parseInt(h.slice(4, 6), 16)
  // 亮度计算（近似）
  const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255
  return luminance < 0.5
}

const currentTheme = ref<ThemeName>(loadTheme())
const customTheme = reactive<CustomTheme>(loadCustom())

function persist() {
  localStorage.setItem(STORAGE_KEY, currentTheme.value)
  localStorage.setItem(CUSTOM_KEY, JSON.stringify(customTheme))
}

watch([currentTheme, customTheme], () => {
  persist()
  if (currentTheme.value === 'custom') {
    applyCustom(customTheme)
  } else {
    applyBuiltin(currentTheme.value)
  }
}, { deep: true, immediate: true })

/** 一键基于主色生成协调主题（自动调整明暗/灰阶） */
function generateFromPrimary(primary: string, mode: 'dark' | 'light' = 'dark') {
  const isDark = mode === 'dark'
  const t = isDark
    ? {
        bgPrimary: '#0b1220', bgCard: '#15203a', bgElevated: '#1f2c4a',
        textPrimary: '#e2e8f0', textSecondary: '#94a3b8', border: '#1e293b',
      }
    : {
        bgPrimary: '#f8fafc', bgCard: '#ffffff', bgElevated: '#e2e8f0',
        textPrimary: '#0f172a', textSecondary: '#475569', border: '#cbd5e1',
      }
  Object.assign(customTheme, { ...t, cyan: primary, name: `基于 ${primary.toUpperCase()}` })
}

export function useTheme() {
  function setTheme(name: ThemeName) {
    currentTheme.value = name
  }
  function resetCustom() {
    Object.assign(customTheme, DEFAULT_CUSTOM_THEME)
  }
  const isCustomActive = computed(() => currentTheme.value === 'custom')
  return {
    currentTheme,
    setTheme,
    themeOptions,
    customTheme,
    isCustomActive,
    generateFromPrimary,
    resetCustom,
  }
}
