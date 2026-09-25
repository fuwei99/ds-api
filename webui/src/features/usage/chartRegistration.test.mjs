// Guards the ECharts on-demand registration in echartsSetup.js.
//
// With `echarts/core` nothing is registered implicitly. A missing module does
// not crash the page: ECharts only reports it on console.error (a missing
// coordinate system is the exception and throws), while the chart itself
// silently renders empty. These tests render the real chart options the usage
// page uses and fail on any such report.
//
// Run with the rest of the Node unit tests via tests/scripts/run-unit-node.sh.
import assert from 'node:assert/strict'
import test from 'node:test'

import { BarChart, LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer, SVGRenderer } from 'echarts/renderers'

import { ECHARTS_MODULES, init, use } from './echartsSetup.js'
import { buildLineOption, buildStackedOption } from './chartOptions.js'

// The app ships the Canvas renderer; rendering in Node needs the SVG one.
// This is additive only — `use` never removes a registration, so it cannot
// mask a module that echartsSetup.js forgot to register.
use([SVGRenderer])

const daily = [
    { key: '2026-09-15', calls: 120, prompt: 4000, output: 1200, total: 5200, cost: 0.12 },
    { key: '2026-09-16', calls: 210, prompt: 6200, output: 2100, total: 8300, cost: 0.19 },
    { key: '2026-09-17', calls: 90, prompt: 3100, output: 800, total: 3900, cost: 0.08 },
    { key: '2026-09-18', calls: 300, prompt: 9000, output: 3400, total: 12400, cost: 0.31 },
    { key: '2026-09-19', calls: 150, prompt: 5200, output: 1600, total: 6800, cost: 0.15 },
]

const tokenSeries = [
    { name: 'Output', color: '#0C70F3', valueOf: (d) => d.output || 0 },
    { name: 'Input', color: '#A0DCFD', valueOf: (d) => d.prompt || 0 },
]

const fmt = (n) => String(n)

function render(option) {
    const reported = []
    const originalError = console.error
    console.error = (...args) => { reported.push(args.join(' ')) }
    try {
        const chart = init(null, null, { renderer: 'svg', ssr: true, width: 640, height: 272 })
        chart.setOption(option)
        const svg = chart.renderToSVGString()
        const model = chart.getModel()
        // getComponent is the only way to observe a component registration that
        // renders without a trace in the output, such as the tooltip.
        const registered = {
            grid: !!model.getComponent('grid'),
            tooltip: !!model.getComponent('tooltip'),
        }
        chart.dispose()
        return { svg, reported, registered }
    } finally {
        console.error = originalError
    }
}

function countOf(svg, tag) {
    return (svg.match(new RegExp(`<${tag}`, 'g')) || []).length
}

test('stacked bar option renders with every needed module registered', () => {
    const { svg, reported, registered } = render(buildStackedOption({
        daily,
        series: tokenSeries,
        fmt,
        fmtCompact: fmt,
        fmtMoney: fmt,
    }))

    assert.deepEqual(reported, [], 'ECharts reported a missing module')
    assert.equal(registered.grid, true, 'GridComponent is not registered')
    assert.equal(registered.tooltip, true, 'TooltipComponent is not registered')
    // Two stacked series over five days, plus axes and axis labels.
    assert.ok(countOf(svg, 'path') >= 10, `expected stacked bars, got ${svg}`)
    assert.ok(countOf(svg, 'text') >= 6, `expected axis labels, got ${svg}`)
})

test('line option renders with every needed module registered', () => {
    const { svg, reported, registered } = render(buildLineOption({
        daily,
        name: 'API Requests',
        fmt,
    }))

    assert.deepEqual(reported, [], 'ECharts reported a missing module')
    assert.equal(registered.grid, true, 'GridComponent is not registered')
    assert.equal(registered.tooltip, true, 'TooltipComponent is not registered')
    // The line itself, its area fill, the symbols and the axes.
    assert.ok(countOf(svg, 'path') >= 4, `expected line and axes, got ${svg}`)
    assert.ok(countOf(svg, 'text') >= 6, `expected axis labels, got ${svg}`)
})

test('cost mode stacked option renders with every needed module registered', () => {
    const { svg, reported } = render(buildStackedOption({
        daily,
        series: [{ name: 'Cost', color: '#0C70F3', valueOf: () => 0, valueOfCost: (d) => d.cost || 0 }],
        costMode: true,
        fmt,
        fmtCompact: fmt,
        fmtMoney: fmt,
    }))

    assert.deepEqual(reported, [], 'ECharts reported a missing module')
    assert.ok(countOf(svg, 'path') >= 4, `expected cost bars, got ${svg}`)
})

test('registers exactly the modules the usage page needs', () => {
    // CanvasRenderer only runs in a browser, so the SSR renders above cannot
    // cover it. Pin the whole list here: dropping any entry should be a
    // deliberate edit that updates this expectation too.
    assert.deepEqual(ECHARTS_MODULES, [
        BarChart,
        LineChart,
        GridComponent,
        TooltipComponent,
        CanvasRenderer,
    ])
})
