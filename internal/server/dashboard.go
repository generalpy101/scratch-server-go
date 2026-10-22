package server

import (
	"fmt"
	"net/http"
)

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, dashboardPage)
}

// dashboardPage is the monitoring UI served at /dashboard.
//
// It is a single self-contained HTML page: the graphs are drawn on
// <canvas> elements by hand (no chart library, works offline). The page
// polls /debug/history every 2 seconds and redraws the graphs with the
// samples the monitor collected in the background.
//
// (This is a raw string literal, so the HTML/JS inside must not use
// backticks.)
const dashboardPage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>pygoserver monitor</title>
<style>
    body {
        margin: 0;
        padding: 40px;
        box-sizing: border-box;
        font-family: Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        background: #0f1117;
        color: #e8eaf0;
    }
    h1 { margin: 0 0 24px; }
    h2 { margin: 0 0 8px; font-size: 14px; color: #8b93a7; font-weight: 600; }
    .controls {
        display: flex;
        align-items: center;
        gap: 12px;
        margin-bottom: 24px;
    }
    button {
        padding: 10px 16px;
        border: none;
        border-radius: 10px;
        background: #3b82f6;
        color: #ffffff;
        font-weight: 600;
        cursor: pointer;
    }
    button:hover { background: #2563eb; }
    button:disabled { background: #292e3b; cursor: wait; }
    .hint { color: #8b93a7; font-size: 13px; }
    #benchmark-output {
        display: none;
        margin: 0 0 24px;
        padding: 18px;
        background: #11141b;
        border: 1px solid #252a35;
        border-radius: 10px;
        font-family: Consolas, monospace;
        font-size: 12px;
        white-space: pre-wrap;
        max-height: 320px;
        overflow-y: auto;
    }
    .charts {
        display: grid;
        grid-template-columns: repeat(auto-fit, minmax(560px, 1fr));
        gap: 16px;
    }
    .chart {
        padding: 16px;
        background: #181b24;
        border: 1px solid #292e3b;
        border-radius: 16px;
    }
    .legend { margin-top: 8px; font-size: 12px; color: #8b93a7; }
    .legend-item { margin-right: 16px; }
    .dot {
        display: inline-block;
        width: 8px;
        height: 8px;
        border-radius: 50%;
        margin-right: 6px;
    }
</style>
</head>
<body>

<h1>pygoserver monitor</h1>

<div class="controls">
    <button id="run-benchmark" type="button">Run benchmark</button>
    <span class="hint">runs all 9 scenarios, 2s each, 50 workers — watch the graphs spike</span>
</div>

<pre id="benchmark-output"></pre>

<div class="charts">
    <div class="chart">
        <h2>CPU</h2>
        <canvas id="chart-cpu" width="560" height="160"></canvas>
        <div class="legend" id="chart-cpu-legend"></div>
    </div>
    <div class="chart">
        <h2>Memory</h2>
        <canvas id="chart-mem" width="560" height="160"></canvas>
        <div class="legend" id="chart-mem-legend"></div>
    </div>
    <div class="chart">
        <h2>Goroutines / OS threads</h2>
        <canvas id="chart-goroutines" width="560" height="160"></canvas>
        <div class="legend" id="chart-goroutines-legend"></div>
    </div>
    <div class="chart">
        <h2>GC CPU</h2>
        <canvas id="chart-gc" width="560" height="160"></canvas>
        <div class="legend" id="chart-gc-legend"></div>
    </div>
</div>

<script>
const MB = 1024 * 1024;

//
// drawChart draws the given series of the history onto a canvas as
// simple line graphs. A series is { key, color, label, divideBy }:
// "key" picks the field out of each sample, "divideBy" converts units
// (e.g. bytes -> MB).
//
function drawChart(canvasId, history, series) {
    const canvas = document.getElementById(canvasId);
    const ctx = canvas.getContext("2d");
    const W = canvas.width;
    const H = canvas.height;
    const padLeft = 44;
    const padBottom = 24;

    ctx.clearRect(0, 0, W, H);

    // Rebuild the legend under the chart.
    const legend = document.getElementById(canvasId + "-legend");
    legend.innerHTML = "";
    series.forEach((s) => {
        const item = document.createElement("span");
        item.className = "legend-item";
        item.innerHTML = "<span class='dot' style='background:" + s.color + "'></span>" + s.label;
        legend.appendChild(item);
    });

    if (history.length < 2) {
        ctx.fillStyle = "#8b93a7";
        ctx.fillText("collecting data...", padLeft, H / 2);
        return;
    }

    // The y-axis max is the largest value across all series, plus a
    // little headroom so the line never touches the top edge.
    let max = 0;
    series.forEach((s) => {
        history.forEach((p) => {
            const v = p[s.key] / (s.divideBy || 1);
            if (v > max) max = v;
        });
    });
    if (max === 0) max = 1;
    max *= 1.1;

    // Map data indexes/values to canvas coordinates.
    const x = (i) => padLeft + (i / (history.length - 1)) * (W - padLeft - 10);
    const y = (v) => H - padBottom - (v / max) * (H - padBottom - 10);

    // Horizontal grid lines with value labels on the left.
    ctx.strokeStyle = "#292e3b";
    ctx.fillStyle = "#8b93a7";
    ctx.font = "11px sans-serif";
    ctx.lineWidth = 1;
    for (let i = 0; i <= 3; i++) {
        const v = (max / 3) * i;
        ctx.beginPath();
        ctx.moveTo(padLeft, y(v));
        ctx.lineTo(W - 10, y(v));
        ctx.stroke();
        ctx.fillText(v.toFixed(v < 10 ? 1 : 0), 4, y(v) + 4);
    }

    // Time range label at the bottom.
    const oldest = new Date(history[0].time * 1000);
    const newest = new Date(history[history.length - 1].time * 1000);
    const hhmmss = (d) => d.toTimeString().slice(0, 8);
    ctx.fillText(hhmmss(oldest), padLeft, H - 8);
    ctx.fillText(hhmmss(newest), W - 70, H - 8);

    // One polyline per series.
    series.forEach((s) => {
        ctx.strokeStyle = s.color;
        ctx.lineWidth = 2;
        ctx.beginPath();
        history.forEach((p, i) => {
            const v = p[s.key] / (s.divideBy || 1);
            if (i === 0) {
                ctx.moveTo(x(i), y(v));
            } else {
                ctx.lineTo(x(i), y(v));
            }
        });
        ctx.stroke();
    });
}

// poll fetches the collected samples and redraws every chart.
async function poll() {
    try {
        const res = await fetch("/debug/history");
        const history = await res.json();

        drawChart("chart-cpu", history, [
            { key: "cpu", color: "#3b82f6", label: "cpu %" },
        ]);
        drawChart("chart-mem", history, [
            { key: "rss", color: "#4ade80", label: "rss (MB)", divideBy: MB },
            { key: "heap_sys", color: "#8b93a7", label: "heap sys (MB)", divideBy: MB },
            { key: "heap_alloc", color: "#fbbf24", label: "heap alloc (MB)", divideBy: MB },
        ]);
        drawChart("chart-goroutines", history, [
            { key: "goroutines", color: "#3b82f6", label: "goroutines" },
            { key: "threads", color: "#f87171", label: "os threads" },
        ]);
        drawChart("chart-gc", history, [
            { key: "gc_cpu", color: "#c084fc", label: "gc cpu %" },
        ]);
    } catch (err) {
        console.error("poll failed:", err);
    }
}

// The benchmark button POSTs to the monitor, which runs the benchmark
// command. It takes ~20 seconds, during which the graphs show the load.
const runBtn = document.getElementById("run-benchmark");
const out = document.getElementById("benchmark-output");

runBtn.addEventListener("click", async () => {
    runBtn.disabled = true;
    out.style.display = "block";
    out.textContent = "running benchmark (about 20 seconds)... watch the graphs!";

    try {
        const res = await fetch("/debug/benchmark", { method: "POST" });
        out.textContent = await res.text();
    } catch (err) {
        out.textContent = "benchmark request failed: " + err;
    }

    runBtn.disabled = false;
});

poll();
setInterval(poll, 2000);
</script>

</body>
</html>
`
