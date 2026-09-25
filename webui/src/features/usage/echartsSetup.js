// Single source of truth for the ECharts on-demand registration used by the
// usage page.
//
// The tree-shakable `echarts/core` entry point registers nothing implicitly, so
// every chart type, component and renderer the charts rely on must be listed in
// ECHARTS_MODULES. Anything missing is reported by ECharts on console.error as
// "[ECharts] ... is used but not imported" (a missing coordinate system throws
// instead) and the chart renders empty. chartRegistration.test.mjs guards this
// list, so add new chart types / components here and there together.
//
// `init` / `use` are re-exported as named bindings on purpose. Re-exporting the
// whole `echarts` namespace (`import * as echarts` + `export default echarts`)
// through an intermediate module defeats property-level tree-shaking and costs
// roughly 13 kB in the built bundle.
import { init, use } from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

export const ECHARTS_MODULES = [
    BarChart,
    LineChart,
    GridComponent,
    TooltipComponent,
    CanvasRenderer,
]

use(ECHARTS_MODULES)

export { init, use }
