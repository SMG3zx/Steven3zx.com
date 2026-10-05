const clock = document.querySelector("#clock");
const year = document.querySelector("#year");
const sampleLimit = 900;
const samples = [];
let chartWindow = 60;
let lastSampleAt = 0;

function updateClock() {
  clock.textContent = new Intl.DateTimeFormat(undefined, {
    hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false,
  }).format(new Date());
}

function formatBytes(bytes, precision = 1) {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let index = -1;
  do { value /= 1024; index += 1; } while (value >= 1024 && index < units.length - 1);
  return `${value.toFixed(precision)} ${units[index]}`;
}

function formatRate(bytes) {
  return `${(bytes / 1024).toFixed(bytes < 10240 ? 1 : 0)} KiB/s`;
}

function formatDuration(seconds) {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  return days ? `${days}d ${hours}h` : hours ? `${hours}h ${minutes}m` : `${minutes}m ${secs}s`;
}

function plotPoints(values, maximum, height, width = 1000) {
  if (!values.length) return "";
  const denominator = Math.max(1, values.length - 1);
  return values.map((value, index) => {
    const x = index / denominator * width;
    const y = height - Math.min(height, Math.max(0, value / maximum * height));
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(" ");
}

function areaPath(points, height, width = 1000) {
  if (!points) return "";
  return `M0 ${height} L${points.replaceAll(" ", " L")} L${width} ${height} Z`;
}

function drawCharts() {
  const now = Date.now();
  const visible = samples.filter((sample) => now - sample.timestamp <= chartWindow * 1000);
  const cpu = plotPoints(visible.map((sample) => sample.cpuPercent), 100, 220);
  const memory = plotPoints(visible.map((sample) => sample.memoryPercent), 100, 220);
  for (const [id, value] of [["cpu-line", cpu], ["memory-line", memory]]) document.querySelector(`#${id}`).setAttribute("points", value);
  for (const [id, value] of [["cpu-area", cpu], ["memory-area", memory]]) document.querySelector(`#${id}`).setAttribute("d", areaPath(value, 220));
  const placeDot = (id, values) => {
    const dot = document.querySelector(`#${id}`);
    if (!values.length) return;
    const [x, y] = values.at(-1).split(",");
    dot.setAttribute("cx", x);
    dot.setAttribute("cy", y);
  };
  placeDot("cpu-dot", cpu.split(" "));
  placeDot("memory-dot", memory.split(" "));

  const topRate = Math.max(1024, ...visible.flatMap((sample) => [sample.receiveBytesPerSecond, sample.sendBytesPerSecond]));
  const rateScale = Math.ceil(topRate / 1024) * 1024;
  const receive = plotPoints(visible.map((sample) => sample.receiveBytesPerSecond), rateScale, 150);
  const send = plotPoints(visible.map((sample) => sample.sendBytesPerSecond), rateScale, 150);
  for (const [id, value] of [["receive-line", receive], ["send-line", send]]) document.querySelector(`#${id}`).setAttribute("points", value);
  for (const [id, value] of [["receive-area", receive], ["send-area", send]]) document.querySelector(`#${id}`).setAttribute("d", areaPath(value, 150));
  document.querySelector("#net-max").textContent = `${(rateScale / 1024).toFixed(0)} KiB/s`;
  if (visible.length) {
    const start = new Date(visible[0].timestamp);
    document.querySelector("#chart-start").textContent = start.toLocaleTimeString([], { minute: "2-digit", second: "2-digit" });
    document.querySelector("#chart-mid").textContent = `${visible.length} SAMPLES · ${chartWindow / 60} MIN WINDOW`;
  }
}

function updateReadouts(sample) {
  document.querySelector("#cpu-current").textContent = `${sample.cpuPercent.toFixed(1)}%`;
  document.querySelector("#cpu-detail").textContent = `${sample.logicalCores} logical cores · system load`;
  document.querySelector("#memory-current").textContent = `${sample.memoryPercent.toFixed(1)}%`;
  document.querySelector("#memory-detail").textContent = `${formatBytes(sample.memoryUsedBytes)} / ${formatBytes(sample.memoryTotalBytes)}`;
  document.querySelector("#network-current").textContent = `${formatRate(sample.receiveBytesPerSecond)} ↓`;
  document.querySelector("#network-detail").textContent = `${formatRate(sample.sendBytesPerSecond)} ↑ sent`;
  document.querySelector("#uptime-current").textContent = formatDuration(sample.uptimeSeconds);
  document.querySelector("#runtime-detail").textContent = `${sample.processCount.toLocaleString()} processes · ${sample.goRoutines} Go routines`;
  document.querySelector("#process-current").textContent = formatBytes(sample.processBytes);
  document.querySelector("#process-count").textContent = sample.processCount.toLocaleString();
  document.querySelector("#goroutines").textContent = sample.goRoutines.toLocaleString();
  document.querySelector("#metrics-state").textContent = "ORIGIN ONLINE";
  document.querySelector("#metrics-updated").textContent = `Updated ${new Date(sample.at).toLocaleTimeString()}`;
  document.querySelector("#metrics-state").classList.add("is-online");
}

async function pollMetrics() {
  try {
    const response = await fetch("/api/metrics", { cache: "no-store" });
    if (!response.ok) throw new Error(`Metrics returned ${response.status}`);
    const sample = await response.json();
    const timestamp = Date.parse(sample.at);
    if (timestamp > lastSampleAt) {
      samples.push({ ...sample, timestamp });
      lastSampleAt = timestamp;
      if (samples.length > sampleLimit) samples.splice(0, samples.length - sampleLimit);
    }
    updateReadouts(sample);
    drawCharts();
  } catch {
    document.querySelector("#metrics-state").textContent = "ORIGIN UNAVAILABLE";
    document.querySelector("#metrics-state").classList.remove("is-online");
    document.querySelector("#metrics-updated").textContent = "Retrying connection";
  }
}

document.querySelectorAll("[data-window]").forEach((button) => button.addEventListener("click", () => {
  chartWindow = Number(button.dataset.window);
  document.querySelectorAll("[data-window]").forEach((item) => item.setAttribute("aria-pressed", String(item === button)));
  drawCharts();
}));

document.querySelectorAll(".nav-item").forEach((link) => link.addEventListener("click", () => {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item === link));
}));

updateClock();
year.textContent = new Date().getFullYear();
window.setInterval(updateClock, 1000);
pollMetrics();
window.setInterval(pollMetrics, 1000);
