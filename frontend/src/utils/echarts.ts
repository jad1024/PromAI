// ECharts 按需注册：只打包项目实际使用的图表类型与组件，
// 替代各页面 `import * as echarts from 'echarts'` 的全量引入（全量 ~1MB）。
import { use } from 'echarts/core'
import { LineChart, BarChart, PieChart, CustomChart } from 'echarts/charts'
import {
  TooltipComponent,
  LegendComponent,
  GridComponent,
  TitleComponent,
  DatasetComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

// CustomChart 必须显式注册：AlertEvents.vue 的「故障时间轴」用 type:'custom'
// 自定义 renderItem 画甘特横杠。P0 按需引入时漏注册会导致该图整块空白。
use([
  LineChart, BarChart, PieChart, CustomChart,
  TooltipComponent, LegendComponent, GridComponent, TitleComponent, DatasetComponent,
  CanvasRenderer,
])

// 把 core 的 init/graphic/ECharts 类型等原样透出，页面可继续 `import * as echarts`。
export * from 'echarts/core'
