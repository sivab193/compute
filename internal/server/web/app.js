// Dashboard client. Polls the aggregate/nodes/jobs endpoints and streams logs
// over SSE. A dashboard token, if configured, is read from localStorage
// (set it once via the browser console: localStorage.poolToken = "...").
const TOKEN = localStorage.getItem("poolToken") || "";
const auth = TOKEN ? { Authorization: "Bearer " + TOKEN } : {};
const qTok = TOKEN ? ("?token=" + encodeURIComponent(TOKEN)) : "";

const $ = (id) => document.getElementById(id);

function mb(v) {
  if (!v) return "0";
  if (v >= 1024 * 1024) return (v / 1024 / 1024).toFixed(1) + " TB";
  if (v >= 1024) return (v / 1024).toFixed(1) + " GB";
  return v + " MB";
}

async function api(path) {
  const r = await fetch(path, { headers: auth });
  if (!r.ok) throw new Error(r.status + " " + path);
  return r.json();
}

async function refresh() {
  try {
    const [agg, nodes, jobs, usage] = await Promise.all([
      api("/api/aggregate"),
      api("/api/nodes"),
      api("/api/jobs"),
      api("/api/usage"),
    ]);
    renderAgg(agg);
    renderNodes(nodes);
    renderJobs(jobs);
    renderUsage(usage);
    setConn(true);
  } catch (e) {
    setConn(false);
  }
}

function renderUsage(u) {
  const box = $("usage");
  if (!box) return;
  const kv = (m) => Object.entries(m || {}).map(([k, v]) => `${k}: ${v}`).join(" · ") || "—";
  box.innerHTML = `
    <div class="node-meta">
      total jobs: ${u.total_jobs} · slow-lane (swap) runs: ${u.slow_lane_jobs}<br>
      by state — ${kv(u.by_state)}<br>
      by runtime — ${kv(u.by_runtime)}<br>
      by node — ${kv(u.by_node)}
    </div>`;
}

function setConn(ok) {
  const c = $("conn");
  c.textContent = ok ? "● live" : "● disconnected";
  c.className = "conn " + (ok ? "ok" : "bad");
}

function renderAgg(a) {
  $("s-cpu").textContent = a.vcpus;
  $("s-gpu").textContent = a.gpu_count;
  $("s-vram").textContent = mb(a.cuda_vram_mb) + " / " + mb(a.metal_vram_mb);
  $("s-ram").textContent = mb(a.ram_total_mb);
  $("s-swap").textContent = mb(a.swap_total_mb);
  $("s-disk").textContent = mb(a.disk_total_mb);
  $("s-nodes").textContent = a.online_nodes + " / " + a.offline_nodes;
  $("s-jobs").textContent = a.running_jobs + " / " + a.queued_jobs;
}

function nodeCard(n) {
  const el = document.createElement("div");
  el.className = "node" + (n.status === "online" ? "" : " offline");
  const gpuBadge = (n.labels && n.labels["gpu-type"] && n.labels["gpu-type"] !== "none")
    ? `<span class="badge gpu">${n.labels["gpu-type"]}</span>` : "";
  const preemptBadge = (n.labels && n.labels.preemptible === "true")
    ? `<span class="badge preempt">preemptible</span>` : "";
  const loadPct = n.cpu_count ? Math.min(100, (n.load_avg_1 / n.cpu_count) * 100) : 0;
  const gpuList = (n.gpus || []).map(g => `${g.model}${g.vram_mb ? " · " + mb(g.vram_mb) : ""}`).join(", ") || "no GPU";
  el.innerHTML = `
    <div class="node-head">
      <span class="node-name">${n.name}</span>
      <span>
        <span class="badge ${n.status}">${n.status}</span>
        ${gpuBadge}${preemptBadge}
      </span>
    </div>
    <div class="node-meta">
      ${n.cpu_count} vCPU · load ${n.load_avg_1.toFixed(2)} · ${n.running_jobs} running
      <div class="bar"><span style="width:${loadPct}%"></span></div>
      RAM ${mb(n.ram_free_mb)} free / ${mb(n.ram_total_mb)} · swap ${mb(n.swap_total_mb)}
      <br>disk ${mb(n.disk_free_mb)} free / ${mb(n.disk_total_mb)} · ${gpuList}
      <br>arch=${(n.labels||{}).arch||"?"} os=${(n.labels||{}).os||"?"} ${n.docker_available ? "· docker" : ""}
    </div>`;
  return el;
}

function renderNodes(nodes) {
  const online = $("nodes"), offline = $("nodes-offline");
  online.innerHTML = ""; offline.innerHTML = "";
  let anyOff = false;
  nodes.forEach(n => {
    if (n.status === "online") online.appendChild(nodeCard(n));
    else { offline.appendChild(nodeCard(n)); anyOff = true; }
  });
  if (!nodes.some(n => n.status === "online"))
    online.innerHTML = '<div class="node-meta">No online nodes. Start an agent to join the pool.</div>';
  $(".offline-h") && ($(".offline-h").style.display = anyOff ? "block" : "none");
  document.querySelector(".offline-h").style.display = anyOff ? "block" : "none";
}

function renderJobs(jobs) {
  const body = $("jobs-body");
  body.innerHTML = "";
  jobs.forEach(j => {
    const tr = document.createElement("tr");
    const lane = j.lane === "slow" ? '<span class="lane slow">slow</span>' : (j.lane || "—");
    const exit = j.exit_code === null || j.exit_code === undefined ? "—" : j.exit_code;
    tr.innerHTML = `
      <td title="${j.id}">${j.id.slice(0, 8)}</td>
      <td>${j.name}</td>
      <td>${j.runtime}</td>
      <td><span class="state ${j.state}">${j.state}</span></td>
      <td>${j.assigned_node || "—"}</td>
      <td>${lane}</td>
      <td>${exit}</td>
      <td>${j.artifact_count || 0}</td>
      <td><button class="linkbtn" data-id="${j.id}" data-name="${j.name}">logs</button></td>`;
    body.appendChild(tr);
  });
  body.querySelectorAll(".linkbtn").forEach(b =>
    b.addEventListener("click", () => openLogs(b.dataset.id, b.dataset.name)));
}

// ---- Log modal + SSE ------------------------------------------------------
let es = null;
function openLogs(id, name) {
  $("log-title").textContent = "Logs · " + name;
  $("log-body").innerHTML = "";
  $("artifact-list").innerHTML = "";
  $("log-modal").classList.add("open");

  if (es) es.close();
  es = new EventSource(`/api/jobs/${id}/logs/stream${qTok}`);
  es.onmessage = (ev) => {
    const l = JSON.parse(ev.data);
    const line = document.createElement("div");
    line.className = l.stream;
    line.textContent = l.text;
    $("log-body").appendChild(line);
    $("log-body").scrollTop = $("log-body").scrollHeight;
  };
  es.addEventListener("done", () => { es.close(); loadArtifacts(id); });
  es.onerror = () => { /* keep modal open; stream may have ended */ };
}

async function loadArtifacts(id) {
  try {
    const files = await api(`/api/jobs/${id}/artifacts`);
    const box = $("artifact-list");
    if (!files.length) { box.innerHTML = '<span class="node-meta">No artifacts.</span>'; return; }
    box.innerHTML = "<span class='node-meta'>Artifacts: </span>";
    files.forEach(f => {
      const a = document.createElement("a");
      a.href = `/api/jobs/${id}/artifacts/${encodeURIComponent(f)}${qTok}`;
      a.textContent = f;
      box.appendChild(a);
    });
  } catch (_) {}
}

$("log-close").addEventListener("click", () => {
  $("log-modal").classList.remove("open");
  if (es) es.close();
});

// ---- Submit ---------------------------------------------------------------
$("submit-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const msg = $("submit-msg");
  msg.textContent = "submitting…"; msg.className = "formmsg";
  const fd = new FormData();
  for (const f of $("f-files").files) fd.append("files", f);
  const add = (k, v) => { if (v !== "" && v !== false) fd.append(k, v); };
  add("needs_gpu", $("f-gpu").value);
  add("min_ram_mb", $("f-ram").value);
  add("timeout_sec", $("f-timeout").value);
  add("needs_arch", $("f-arch").value);
  add("needs_os", $("f-os").value);
  if ($("f-preempt").checked) fd.append("allow_preempt", "true");
  if ($("f-disk").checked) fd.append("disk_backed", "true");
  try {
    const r = await fetch("/api/jobs", { method: "POST", headers: auth, body: fd });
    const j = await r.json();
    if (!r.ok) throw new Error(j.error || r.status);
    msg.textContent = "queued job " + j.id.slice(0, 8);
    msg.className = "formmsg ok";
    $("submit-form").reset();
    refresh();
  } catch (err) {
    msg.textContent = "error: " + err.message;
    msg.className = "formmsg err";
  }
});

refresh();
setInterval(refresh, 3000);
